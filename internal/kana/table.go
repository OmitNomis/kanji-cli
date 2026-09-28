package kana

// romajiTable maps romaji syllables to hiragana. It is deliberately permissive:
// Hepburn ("shi", "chi", "tsu", "fu", "ji"), Kunrei ("si", "ti", "tu", "hu",
// "zi") and the wapuro spellings an IME accepts ("sya", "jya", "cya") all land
// on the same kana, because the point of this program is to test whether you
// know the reading, not whether you know a romanisation standard.
//
// Sokuon (っ), syllabic ん and the long-vowel mark are not in here; they are
// handled structurally in ToHiragana.
var romajiTable = map[string]string{
	"a": "あ", "i": "い", "u": "う", "e": "え", "o": "お",

	// k / g
	"ka": "か", "ki": "き", "ku": "く", "ke": "け", "ko": "こ",
	"ga": "が", "gi": "ぎ", "gu": "ぐ", "ge": "げ", "go": "ご",
	"kya": "きゃ", "kyi": "きぃ", "kyu": "きゅ", "kye": "きぇ", "kyo": "きょ",
	"gya": "ぎゃ", "gyi": "ぎぃ", "gyu": "ぎゅ", "gye": "ぎぇ", "gyo": "ぎょ",
	"kwa": "くぁ", "kwi": "くぃ", "kwu": "くぅ", "kwe": "くぇ", "kwo": "くぉ",
	"gwa": "ぐぁ", "gwi": "ぐぃ", "gwu": "ぐぅ", "gwe": "ぐぇ", "gwo": "ぐぉ",

	// s / z, including the shi/si and ji/zi families
	"sa": "さ", "si": "し", "shi": "し", "su": "す", "se": "せ", "so": "そ",
	"za": "ざ", "zi": "じ", "ji": "じ", "zu": "ず", "ze": "ぜ", "zo": "ぞ",
	"sha": "しゃ", "shu": "しゅ", "she": "しぇ", "sho": "しょ",
	"sya": "しゃ", "syi": "しぃ", "syu": "しゅ", "sye": "しぇ", "syo": "しょ",
	"ja": "じゃ", "ju": "じゅ", "je": "じぇ", "jo": "じょ",
	"jya": "じゃ", "jyi": "じぃ", "jyu": "じゅ", "jye": "じぇ", "jyo": "じょ",
	"zya": "じゃ", "zyi": "じぃ", "zyu": "じゅ", "zye": "じぇ", "zyo": "じょ",
	"swa": "すぁ", "swi": "すぃ", "swu": "すぅ", "swe": "すぇ", "swo": "すぉ",

	// t / d, including the chi/ti and tsu/tu families
	"ta": "た", "ti": "ち", "chi": "ち", "tsu": "つ", "tu": "つ", "te": "て", "to": "と",
	"da": "だ", "di": "ぢ", "du": "づ", "de": "で", "do": "ど",
	"cha": "ちゃ", "chu": "ちゅ", "che": "ちぇ", "cho": "ちょ",
	"cya": "ちゃ", "cyi": "ちぃ", "cyu": "ちゅ", "cye": "ちぇ", "cyo": "ちょ",
	"tya": "ちゃ", "tyi": "ちぃ", "tyu": "ちゅ", "tye": "ちぇ", "tyo": "ちょ",
	"dya": "ぢゃ", "dyi": "ぢぃ", "dyu": "ぢゅ", "dye": "ぢぇ", "dyo": "ぢょ",
	"tsa": "つぁ", "tsi": "つぃ", "tse": "つぇ", "tso": "つぉ",
	"tha": "てゃ", "thi": "てぃ", "thu": "てゅ", "the": "てぇ", "tho": "てょ",
	"dha": "でゃ", "dhi": "でぃ", "dhu": "でゅ", "dhe": "でぇ", "dho": "でょ",
	"twa": "とぁ", "twi": "とぃ", "twu": "とぅ", "twe": "とぇ", "two": "とぉ",
	"dwa": "どぁ", "dwi": "どぃ", "dwu": "どぅ", "dwe": "どぇ", "dwo": "どぉ",

	// n
	"na": "な", "ni": "に", "nu": "ぬ", "ne": "ね", "no": "の",
	"nya": "にゃ", "nyi": "にぃ", "nyu": "にゅ", "nye": "にぇ", "nyo": "にょ",

	// h / b / p, including the fu/hu family
	"ha": "は", "hi": "ひ", "hu": "ふ", "fu": "ふ", "he": "へ", "ho": "ほ",
	"ba": "ば", "bi": "び", "bu": "ぶ", "be": "べ", "bo": "ぼ",
	"pa": "ぱ", "pi": "ぴ", "pu": "ぷ", "pe": "ぺ", "po": "ぽ",
	"hya": "ひゃ", "hyi": "ひぃ", "hyu": "ひゅ", "hye": "ひぇ", "hyo": "ひょ",
	"bya": "びゃ", "byi": "びぃ", "byu": "びゅ", "bye": "びぇ", "byo": "びょ",
	"pya": "ぴゃ", "pyi": "ぴぃ", "pyu": "ぴゅ", "pye": "ぴぇ", "pyo": "ぴょ",
	"fa": "ふぁ", "fi": "ふぃ", "fe": "ふぇ", "fo": "ふぉ",
	"fya": "ふゃ", "fyu": "ふゅ", "fyo": "ふょ",

	// m
	"ma": "ま", "mi": "み", "mu": "む", "me": "め", "mo": "も",
	"mya": "みゃ", "myi": "みぃ", "myu": "みゅ", "mye": "みぇ", "myo": "みょ",

	// y
	"ya": "や", "yi": "い", "yu": "ゆ", "ye": "いぇ", "yo": "よ",

	// r
	"ra": "ら", "ri": "り", "ru": "る", "re": "れ", "ro": "ろ",
	"rya": "りゃ", "ryi": "りぃ", "ryu": "りゅ", "rye": "りぇ", "ryo": "りょ",

	// w
	"wa": "わ", "wi": "うぃ", "wu": "う", "we": "うぇ", "wo": "を",
	"wha": "うぁ", "whi": "うぃ", "whu": "う", "whe": "うぇ", "who": "うぉ",

	// v
	"va": "ゔぁ", "vi": "ゔぃ", "vu": "ゔ", "ve": "ゔぇ", "vo": "ゔぉ",
	"vya": "ゔゃ", "vyu": "ゔゅ", "vyo": "ゔょ",

	// explicit small kana and bare sokuon, as an IME spells them
	"xa": "ぁ", "xi": "ぃ", "xu": "ぅ", "xe": "ぇ", "xo": "ぉ",
	"la": "ぁ", "li": "ぃ", "lu": "ぅ", "le": "ぇ", "lo": "ぉ",
	"xya": "ゃ", "xyu": "ゅ", "xyo": "ょ", "xwa": "ゎ",
	"lya": "ゃ", "lyu": "ゅ", "lyo": "ょ", "lwa": "ゎ",
	"xtu": "っ", "xtsu": "っ", "ltu": "っ", "ltsu": "っ",
	"xka": "ゕ", "xke": "ゖ",
}

// maxRomajiLen is the longest key in romajiTable, so the greedy matcher in
// ToHiragana knows how far ahead to look.
const maxRomajiLen = 4

// vowelRows groups hiragana by the vowel they end in. Used to resolve the
// long-vowel mark ー, which repeats whatever vowel came before it.
var vowelRows = map[rune]string{
	'a': "あかがさざただなはばぱまやらわぁゃゎゕ",
	'i': "いきぎしじちぢにひびぴみりゐぃ",
	'u': "うくぐすずつづぬふぶぷむゆるゔぅゅ",
	'e': "えけげせぜてでねへべぺめれゑぇゖ",
	'o': "おこごそぞとどのほぼぽもよろをぉょ",
}

// kanaToVowel is the inverted vowelRows: kana -> its vowel.
var kanaToVowel = func() map[rune]rune {
	m := make(map[rune]rune, 96)
	for vowel, row := range vowelRows {
		for _, k := range row {
			// Small kana such as ゃ follow a base kana and override its vowel,
			// which is exactly what we want for きょう -> ょ is the o.
			m[k] = vowel
		}
	}
	return m
}()

// plainVowelKana is the kana a ー expands to, per vowel.
var plainVowelKana = map[rune]rune{
	'a': 'あ', 'i': 'い', 'u': 'う', 'e': 'え', 'o': 'お',
}
