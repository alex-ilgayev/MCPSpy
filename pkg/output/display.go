package output

// OutputHandler defines the interface for different output formats
type OutputHandler interface {
	PrintHeader()
	PrintStats(stats map[string]int)
	PrintInfo(format string, args ...interface{})
}
