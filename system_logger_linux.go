//go:build linux

package logkeeper

import "github.com/mongodb/grip/send"

func getSystemLogger() send.Sender {
	sender, err := send.MakeSystemdLogger()
	if err != nil {
		return send.MakeNative()
	}

	return sender
}
