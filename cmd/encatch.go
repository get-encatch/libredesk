package main

// encatch: single entry point for Encatch features. Upstream code only calls
// initEncatch from the end of initHandlers (cmd/handlers.go).

import "github.com/zerodha/fastglue"

func initEncatch(g *fastglue.Fastglue) {
	initEncatchMyTickets(g)
	initEncatchRedact(g)
}
