//go:build !linux
// +build !linux

package logkeeper

import "github.com/mongodb/grip/send"

func getSystemLogger() send.Sender { return send.MakeNative() }
