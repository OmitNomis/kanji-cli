#!/usr/bin/env bash
#
# Install or update cli-kanji on a remote machine.
#
# Run this from the development box; the target needs nothing but an SSH server
# and gzip. Two files get installed -- a static binary and the prebuilt
# dictionary -- and nothing needs configuring afterwards.
#
#   scripts/deploy.sh root@10.0.0.5    # an explicit target
#   scripts/deploy.sh                  # the target in scripts/deploy.env
#
# You are asked for the target's password exactly once, the first time, to
# install a deploy key. Every run after that is passwordless.
#
# The target comes from the argument, then $KANJI_HOST, then KANJI_HOST in
# scripts/deploy.env, which is untracked so a machine address never ends up in
# the repository.
#
# Environment:
#   WITH_IMPORTER=1   also install kanjidata, so the target can rebuild the
#                     dictionary itself when EDRDG publishes a new release
#   WITH_BOT=1        also install kanji-bot and its systemd unit, which is the
#                     Telegram front-end and the one thing here that is a daemon
#   WITH_GREETING=1   print the due-review count when you log in to the target
#   NO_KEY=1          skip key setup and let ssh prompt for each operation
#
# The bot needs a token, which this script never sees: the first WITH_BOT=1
# deploy writes /etc/cli-kanji/bot.env with placeholders at mode 0600 and leaves
# the service stopped until you fill it in. Later deploys do not touch it.
#
# Safe to re-run. The dictionary is only sent when it differs from the one
# already installed, so a routine update moves 6 MB instead of 18 MB.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

# Local defaults, untracked, so a machine address never ends up in the repo.
# shellcheck source=/dev/null
[[ -f scripts/deploy.env ]] && . scripts/deploy.env

TARGET="${1:-${KANJI_HOST:-}}"
WITH_IMPORTER="${WITH_IMPORTER:-0}"
WITH_BOT="${WITH_BOT:-0}"
WITH_GREETING="${WITH_GREETING:-0}"
NO_KEY="${NO_KEY:-0}"

DICT_SRC="data/dict.db"
SSH_KEY="${KANJI_SSH_KEY:-$HOME/.ssh/id_ed25519_atom}"

# Overridable so the script can be exercised against a stubbed target without
# writing to a real /usr/local.
STAGING="${KANJI_STAGING:-/tmp/cli-kanji-deploy}"
BIN_DIR="${KANJI_BIN_DIR:-/usr/local/bin}"

SSH_OPTS=(-o ConnectTimeout=20 -o ServerAliveInterval=15 -o StrictHostKeyChecking=accept-new)

say()  { printf '\n\033[1m==\033[0m \033[1m%s\033[0m\n' "$*"; }
note() { printf '   %s\n' "$*"; }
warn() { printf '   \033[33m!\033[0m %s\n' "$*"; }
die()  { printf '\n\033[31m!!\033[0m %s\n' "$*" >&2; exit 1; }

remote() { ssh "${SSH_OPTS[@]}" "$TARGET" "$@"; }

# rsh runs a script read from stdin on the target, which avoids a second level
# of shell quoting for anything non-trivial.
rsh() { ssh "${SSH_OPTS[@]}" "$TARGET" 'sh -s'; }

[[ -n "$TARGET" ]] || die "no target: pass user@host, set KANJI_HOST, or put KANJI_HOST=user@host in scripts/deploy.env"

# ------------------------------------------------------------------ 1. key

if [[ "$NO_KEY" != 1 ]]; then
	say "deploy key"

	if [[ ! -f "$SSH_KEY" ]]; then
		ssh-keygen -t ed25519 -N "" -C "cli-kanji deploy" -f "$SSH_KEY" >/dev/null
		note "generated $SSH_KEY"
	fi
	SSH_OPTS+=(-i "$SSH_KEY")

	if remote -o BatchMode=yes true 2>/dev/null; then
		note "already installed, no password needed"
	else
		note "installing it on $TARGET -- this is the one time you need the password"
		# ssh-copy-id prompts on the terminal, so this is the only step that
		# cannot be automated. Everything after it is key-authenticated.
		ssh-copy-id -i "$SSH_KEY" -o StrictHostKeyChecking=accept-new "$TARGET" \
			|| die "could not install the key; check the password, or re-run with NO_KEY=1"
		remote -o BatchMode=yes true 2>/dev/null \
			|| die "the key was copied but still is not accepted"
		note "done -- you can also now use: ssh -i $SSH_KEY $TARGET"
	fi
fi

# ------------------------------------------------------------ 2. preflight

say "inspecting $TARGET"

# The probe reports key=value pairs rather than bare lines. Parsing by position
# means one missing or empty value silently shifts every field after it, and the
# first thing that happens to the shifted data is a build for the wrong
# architecture.
PROBE="$(rsh <<'EOS'
echo "arch=$(uname -m)"
echo "kernel=$(uname -r)"
echo "home=$HOME"
echo "uid=$(id -u)"
echo "lang=${LC_ALL:-${LC_CTYPE:-${LANG:-unset}}}"
command -v gzip      >/dev/null && echo "gzip=yes"      || echo "gzip=no"
command -v sha256sum >/dev/null && echo "sha256sum=yes" || echo "sha256sum=no"
{ df -Pk /usr/local 2>/dev/null || df -Pk / 2>/dev/null; } |
	awk 'NR==2 {print "free_kb=" $4+0; found=1} END {if (!found) print "free_kb=0"}'
EOS
)"

REMOTE_ARCH= REMOTE_KERNEL= REMOTE_HOME= REMOTE_LANG=unset REMOTE_UID=unknown
HAS_GZIP=no HAS_SHA=no REMOTE_FREE_KB=0
while IFS='=' read -r key value; do
	case "$key" in
		arch)      REMOTE_ARCH="$value" ;;
		kernel)    REMOTE_KERNEL="$value" ;;
		home)      REMOTE_HOME="$value" ;;
		uid)       REMOTE_UID="$value" ;;
		lang)      REMOTE_LANG="$value" ;;
		gzip)      HAS_GZIP="$value" ;;
		sha256sum) HAS_SHA="$value" ;;
		free_kb)   REMOTE_FREE_KB="$value" ;;
	esac
done <<<"$PROBE"

[[ -n "$REMOTE_ARCH" ]] || die "could not read the target architecture"
[[ -n "$REMOTE_HOME" ]] || die "could not read the target home directory"
[[ "$HAS_GZIP" == yes ]] || die "the target has no gzip"

case "$REMOTE_ARCH" in
	x86_64|amd64)        GOARCH=amd64 GOARM= ;;
	i386|i486|i586|i686) GOARCH=386   GOARM= ;;
	aarch64|arm64)       GOARCH=arm64 GOARM= ;;
	armv7l)              GOARCH=arm   GOARM=7 ;;
	armv6l)              GOARCH=arm   GOARM=6 ;;
	*) die "unsupported target architecture: $REMOTE_ARCH" ;;
esac

note "arch     $REMOTE_ARCH  ->  linux/$GOARCH${GOARM:+ (GOARM=$GOARM)}"
note "kernel   $REMOTE_KERNEL"
note "free     $((REMOTE_FREE_KB / 1024)) MB on /usr/local"

# The dictionary is 43 MB and the binary under 7 MB. Refuse early rather than
# leave a truncated database where the review loop will find it.
(( REMOTE_FREE_KB > 120000 )) || die "only $((REMOTE_FREE_KB / 1024)) MB free, want at least 120 MB"

# The dictionary goes in the per-user config directory because that is already
# one of the places the binary looks, which keeps the install free of
# environment variables and profile edits.
DATA_DIR="$REMOTE_HOME/.config/cli-kanji"

# ---------------------------------------------------------------- 3. build

[[ -f "$DICT_SRC" ]] || die "$DICT_SRC is missing -- run 'make dict' first"
command -v go >/dev/null || die "go is not installed on this machine"

say "building for linux/$GOARCH"
mkdir -p dist

# Microsoft Defender flags the cross-compiled Linux kanji-bot as
# Trojan:Linux/Kaiji.A!MTB and deletes it out of Go's build directory before the
# link finishes. Kaiji is a Go-written Linux botnet, so the heuristic hits a
# static Go ELF that speaks HTTP; kanji itself is not flagged. It is a false
# positive, and the fix is a scan exclusion on this repository -- which only
# helps if the build actually happens inside it, so keep Go's temporary
# directory here rather than under AppData.
case "$(uname -s)" in
	MINGW*|MSYS*|CYGWIN*)
		GOTMPDIR="${GOTMPDIR:-$REPO_ROOT/.gotmp}"
		mkdir -p "$GOTMPDIR"
		export GOTMPDIR
		;;
esac

BINARIES=(kanji)
(( WITH_IMPORTER )) && BINARIES+=(kanjidata)

# EXTRA_BINARIES names further cmd/ packages to ship, so a new front-end can be
# deployed without editing this script. A name with no package is skipped
# rather than failing: that is what let EXTRA_BINARIES=kanji-bot be wired up
# before cmd/kanji-bot existed. WITH_BOT=1 is the same thing said more plainly,
# and it is what also installs the unit further down.
EXTRA="${EXTRA_BINARIES:-}"
(( WITH_BOT )) && EXTRA="$EXTRA kanji-bot"

for name in $EXTRA; do
	# A name asked for twice -- WITH_BOT=1 alongside EXTRA_BINARIES=kanji-bot --
	# must not be built and sent twice.
	for have in "${BINARIES[@]}"; do
		[[ "$have" == "$name" ]] && continue 2
	done
	if [[ -d "cmd/$name" ]]; then
		BINARIES+=("$name")
	else
		warn "no cmd/$name in this tree, skipping it"
	fi
done

for name in "${BINARIES[@]}"; do
	out="dist/$name-linux-$GOARCH"
	CGO_ENABLED=0 GOOS=linux GOARCH="$GOARCH" GOARM="${GOARM:-}" GO386=sse2 \
		go build -trimpath -ldflags="-s -w" -o "$out" "./cmd/$name"
	note "$(du -h "$out" | cut -f1)	$out"
done

# ------------------------------------------------------------- 4. transfer

say "staging"
remote "mkdir -p '$STAGING' '$DATA_DIR'"

SEND_DICT=1
if [[ "$HAS_SHA" == yes ]]; then
	local_sum="$(sha256sum "$DICT_SRC" | cut -d' ' -f1)"
	remote_sum="$(remote "sha256sum '$DATA_DIR/dict.db' 2>/dev/null | cut -d' ' -f1" || true)"
	if [[ -n "$remote_sum" && "$local_sum" == "$remote_sum" ]]; then
		SEND_DICT=0
		note "dictionary already current, skipping the transfer"
	fi
else
	warn "no sha256sum on the target: sending the dictionary unverified"
fi

if (( SEND_DICT )); then
	say "sending the dictionary (43 MB raw, 18 MB compressed)"
	# Compressing first is worth it on a relayed link, and costs about a second.
	gzip -c -6 "$DICT_SRC" | remote "cat > '$STAGING/dict.db.gz' && gzip -df '$STAGING/dict.db.gz'"

	if [[ "$HAS_SHA" == yes ]]; then
		got="$(remote "sha256sum '$STAGING/dict.db' | cut -d' ' -f1")"
		[[ "$got" == "$local_sum" ]] || die "dictionary checksum mismatch after transfer"
		note "checksum verified"
	fi
fi

say "sending binaries"
for name in "${BINARIES[@]}"; do
	scp -q "${SSH_OPTS[@]}" "dist/$name-linux-$GOARCH" "$TARGET:$STAGING/$name"
	note "$name"
done

# -------------------------------------------------------------- 5. install

say "installing"
BINARY_LIST="${BINARIES[*]}"
rsh <<EOS
set -e
for name in $BINARY_LIST; do
	cp "$STAGING/\$name" "$BIN_DIR/\$name.new"
	chmod 0755 "$BIN_DIR/\$name.new"
	# Rename over the top so a running copy is never half-replaced.
	mv "$BIN_DIR/\$name.new" "$BIN_DIR/\$name"
done
if [ -f "$STAGING/dict.db" ]; then
	mv "$STAGING/dict.db" "$DATA_DIR/dict.db"
	chmod 0644 "$DATA_DIR/dict.db"
fi
rm -rf "$STAGING"
EOS

for name in "${BINARIES[@]}"; do note "$BIN_DIR/$name"; done
note "$DATA_DIR/dict.db"

# --------------------------------------------------------------- 6. service

# The bot is the one part of this program that has to keep running, so it is
# the one part that needs a unit. Everything here is idempotent: a re-deploy
# rewrites the unit, leaves the token file alone, and restarts.
INSTALLED_BOT=0
for name in "${BINARIES[@]}"; do
	if [[ "$name" == kanji-bot ]]; then INSTALLED_BOT=1; fi
done

if (( INSTALLED_BOT )); then
	say "kanji-bot service"

	if [[ "$REMOTE_UID" != 0 ]]; then
		warn "not root on the target (uid $REMOTE_UID): skipping the unit"
		warn "install it yourself from scripts/kanji-bot.service, or re-run as root"
		INSTALLED_BOT=0
	fi
fi

if (( INSTALLED_BOT )); then
	# Substitute the paths this deploy actually used, so ReadWritePaths matches
	# where the databases really are.
	sed -e "s|@BIN_DIR@|$BIN_DIR|g" -e "s|@DATA_DIR@|$DATA_DIR|g" \
		scripts/kanji-bot.service > dist/kanji-bot.service

	remote "mkdir -p '$STAGING'"
	scp -q "${SSH_OPTS[@]}" dist/kanji-bot.service "$TARGET:$STAGING/kanji-bot.service"

	# The token must never be committed, never be passed as an argument, and
	# never be overwritten by a deploy. A fresh file is created with
	# placeholders; an existing one is left exactly as it is.
	NEEDS_TOKEN="$(rsh <<EOS
set -e
mkdir -p /etc/cli-kanji
if [ ! -f /etc/cli-kanji/bot.env ]; then
	cat > /etc/cli-kanji/bot.env <<'ENV'
# cli-kanji Telegram bot. Mode 0600: this file is a credential.
#
# TELEGRAM_BOT_TOKEN comes from @BotFather.
# TELEGRAM_ALLOWED_CHAT_IDS is who may use the bot, comma separated. Empty
# denies everyone, which is the only safe default. To find your own ID, message
# the bot once and read the rejected ID out of: journalctl -u kanji-bot
TELEGRAM_BOT_TOKEN=CHANGEME
TELEGRAM_ALLOWED_CHAT_IDS=
ENV
fi
chmod 0600 /etc/cli-kanji/bot.env
chown root:root /etc/cli-kanji/bot.env 2>/dev/null || true

install -m 0644 "$STAGING/kanji-bot.service" /etc/systemd/system/kanji-bot.service
systemctl daemon-reload
rm -rf "$STAGING"

if grep -q 'CHANGEME' /etc/cli-kanji/bot.env; then
	echo needs-token
else
	echo ready
fi
EOS
)"

	note "/etc/systemd/system/kanji-bot.service"

	if [[ "$NEEDS_TOKEN" == *needs-token* ]]; then
		# Starting it now would just crash-loop against a placeholder token.
		warn "/etc/cli-kanji/bot.env has no token yet, so the service was not started"
		note "fill it in:   ssh $TARGET nano /etc/cli-kanji/bot.env"
		note "then:         ssh $TARGET systemctl enable --now kanji-bot"
		INSTALLED_BOT=0
	else
		# enable is idempotent and restart covers the already-running case: the
		# new binary is a new inode, so the old one runs until it is restarted.
		rsh <<EOS
set -e
systemctl enable kanji-bot >/dev/null 2>&1 || true
systemctl restart kanji-bot
EOS
		note "enabled and restarted"

		# An unfilled allowlist is not an error -- the bot runs and refuses
		# everyone, which is how the first chat ID is discovered -- but it is
		# worth saying out loud, because the symptom otherwise is a bot that
		# answers nothing.
		if remote "grep -q '^TELEGRAM_ALLOWED_CHAT_IDS=\$' /etc/cli-kanji/bot.env"; then
			warn "the allowlist is empty, so every chat is refused. Message the bot, then:"
			warn "  ssh $TARGET \"journalctl -u kanji-bot | grep 'rejected chat'\""
			warn "put that ID in /etc/cli-kanji/bot.env and restart the service"
		fi
	fi
fi

# ------------------------------------------------------------- 7. greeting

if (( WITH_GREETING )); then
	say "login greeting"
	# A marker makes this idempotent, so re-running does not stack up copies.
	rsh <<EOS
set -e
profile="\$HOME/.profile"
[ -f "\$HOME/.bashrc" ] && profile="\$HOME/.bashrc"
marker='# >>> cli-kanji greeting >>>'
if grep -qF "\$marker" "\$profile" 2>/dev/null; then
	echo "   already present in \$profile"
else
	{
		echo ""
		echo "\$marker"
		echo 'if [ -t 1 ] && command -v kanji >/dev/null; then'
		echo '    kanji stats 2>/dev/null | grep -E "due now|last 7 days" || true'
		echo 'fi'
		echo '# <<< cli-kanji greeting <<<'
	} >> "\$profile"
	echo "   added to \$profile"
fi
EOS
fi

# --------------------------------------------------------------- 8. verify

say "verifying"
remote "$BIN_DIR/kanji version" || die "the installed binary does not run"

# Japanese needs a UTF-8 locale to draw. Over SSH the client terminal does the
# drawing, so this only bites on the machine's own console.
case "${REMOTE_LANG,,}" in
	*utf-8*|*utf8*) note "locale   $REMOTE_LANG" ;;
	*)              warn "locale is $REMOTE_LANG, not UTF-8 -- fine over SSH, but kanji may show as boxes on the target's own console" ;;
esac

if (( INSTALLED_BOT )); then
	# is-active only says the process survived five seconds, which a bot with a
	# rejected token does not: it logs the refusal and exits. So the real check
	# is that it got as far as saying who it is.
	if remote "systemctl is-active --quiet kanji-bot"; then
		note "service  kanji-bot is active"
	else
		warn "kanji-bot is not active -- see: ssh $TARGET journalctl -u kanji-bot -n 50"
	fi

	AUTH="$(remote "journalctl -u kanji-bot --since '-2 min' --no-pager 2>/dev/null | grep -m1 'authenticated as' || true")"
	if [[ -n "$AUTH" ]]; then
		note "bot      ${AUTH#*] }"
	else
		warn "the bot has not logged an authentication yet; check the token in /etc/cli-kanji/bot.env"
	fi
fi

say "ready"
note "review now:   ssh $TARGET -t kanji"
note "catch up:     ssh $TARGET -t kanji -cram -new-kanji 40"
note "progress:     ssh $TARGET kanji stats"
note "re-deploy:    scripts/deploy.sh $TARGET"

if (( INSTALLED_BOT )); then
	note "bot log:      ssh $TARGET journalctl -u kanji-bot -f"
	note "bot restart:  ssh $TARGET systemctl restart kanji-bot"
	note "rollback:     ssh $TARGET systemctl stop kanji-bot   # the CLI is untouched"
	warn "test the allowlist by hand: message the bot from another account and"
	warn "confirm it answers nothing but a flat refusal"
fi
