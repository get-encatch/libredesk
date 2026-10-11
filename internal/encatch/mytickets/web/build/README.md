# Build output of the My Tickets app

`pnpm build` (in `internal/encatch/mytickets/web`) writes the production build to
`app/`, which Go embeds into the binary (`spa.go`). The release build does this (see
`.goreleaser.yaml`); `app/` is not committed.

This file is committed so the Go code compiles, and its tests run, without a Node
build. A binary built without `app/` answers `/my-tickets` with 503 and logs why.
