//go:build amd64 && !userland

package ebpf

import _ "embed"

type archObjects = mcpspy_bpfel_x86Objects
var loadArchObjects = loadMcpspy_bpfel_x86Objects
