//go:build amd64

package ebpf

import _ "embed"

type archObjects = mcpspy_bpfel_x86Objects

var loadArchObjects = loadMcpspy_bpfel_x86Objects
