//go:build arm64

package ebpf

import _ "embed"

type archObjects = mcpspy_bpfel_arm64Objects

var loadArchSpec = loadMcpspy_bpfel_arm64
