package message

import "embed"

//go:embed assets/message.txt
var Text string

//go:embed assets/nested
var Files embed.FS

//go:embed assets/bytes.bin
var Bytes []byte

//go:embed all:assets
var Hidden embed.FS

//go:embed assets/*.txt
var Glob embed.FS
