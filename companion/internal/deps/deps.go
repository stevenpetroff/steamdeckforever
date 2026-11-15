// Package deps pins third-party modules while packages are being written in
// parallel. Delete it once every dependency is imported by real code.
package deps

import (
	_ "github.com/BurntSushi/toml"
	_ "github.com/coder/websocket"
	_ "github.com/jezek/xgb"
	_ "github.com/jezek/xgb/xfixes"
	_ "github.com/jezek/xgb/xproto"
	_ "github.com/skip2/go-qrcode"
	_ "golang.org/x/sys/unix"
)
