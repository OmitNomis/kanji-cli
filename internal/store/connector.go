package store

import (
	"context"
	"database/sql/driver"
	"fmt"

	"modernc.org/sqlite"
)

// attachConnector opens progress-database connections with the dictionary
// already attached.
//
// ATTACH is per-connection state, and database/sql owns the connections: it can
// discard one that returns driver.ErrBadConn and open a replacement at any
// point. Running the ATTACH once after sql.Open would leave that replacement
// without a dictionary, and the failure would surface much later as "no such
// table: dict.item" in the middle of a session.
//
// A connection hook on a privately constructed driver closes that hole: the
// attach happens as part of opening any connection, so there is no window in
// which one exists without it. The driver is constructed rather than registered
// process-wide so that nothing else inherits the hook.
type attachConnector struct {
	driver *sqlite.Driver
	dsn    string
}

// attachingConnector builds a connector for dsn whose connections all have
// dictPath attached under DictAlias.
func attachingConnector(dsn, dictPath string) driver.Connector {
	d := &sqlite.Driver{}
	d.RegisterConnectionHook(func(conn sqlite.ExecQuerierContext, _ string) error {
		_, err := conn.ExecContext(context.Background(),
			`ATTACH DATABASE ? AS `+DictAlias,
			[]driver.NamedValue{{Ordinal: 1, Value: dictPath}})
		if err != nil {
			return fmt.Errorf("attach dictionary %s: %w", dictPath, err)
		}
		return nil
	})
	return &attachConnector{driver: d, dsn: dsn}
}

func (c *attachConnector) Connect(context.Context) (driver.Conn, error) {
	return c.driver.Open(c.dsn)
}

func (c *attachConnector) Driver() driver.Driver { return c.driver }
