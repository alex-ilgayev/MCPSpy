# Userland MCPSpy Library Makefile

# Compiler and flags
CC = gcc
CFLAGS = -Wall -Wextra -fPIC -g -O2 -std=gnu99
LDFLAGS = -shared -ldl -lpthread

# Optional dependencies (auto-detected)
PCAP_AVAILABLE := $(shell pkg-config --exists libpcap && echo yes || test -f /opt/homebrew/opt/libpcap/lib/libpcap.dylib && echo yes || echo no)
SSL_AVAILABLE := $(shell pkg-config --exists openssl && echo yes || echo no)

# Add optional libraries if available
ifeq ($(PCAP_AVAILABLE),yes)
    ifeq ($(shell pkg-config --exists libpcap && echo yes || echo no),yes)
        CFLAGS += -DHAVE_PCAP $(shell pkg-config --cflags libpcap)
        LDFLAGS += $(shell pkg-config --libs libpcap)
    else
        # macOS Homebrew fallback
        CFLAGS += -DHAVE_PCAP -I/opt/homebrew/opt/libpcap/include
        LDFLAGS += -L/opt/homebrew/opt/libpcap/lib -lpcap
    endif
else
    $(warning libpcap not found - packet capture will be disabled)
endif

ifeq ($(SSL_AVAILABLE),yes)
    CFLAGS += -DHAVE_OPENSSL $(shell pkg-config --cflags openssl)
    LDFLAGS += $(shell pkg-config --libs openssl)
else
    $(warning OpenSSL not found - SSL monitoring will be limited)
endif

# Source files
SOURCES = libmcpspy.c stdio_monitor.c http_monitor.c ssl_monitor.c packet_monitor.c
OBJECTS = $(SOURCES:.c=.o)
TARGET = libmcpspy.so

# Default target
.PHONY: all
all: $(TARGET)

# Build shared library
$(TARGET): $(OBJECTS)
	@echo "Building userland MCPSpy library..."
	$(CC) $(OBJECTS) -o $(TARGET) $(LDFLAGS)
	@echo "Library built: $(TARGET)"

# Compile source files
%.o: %.c libmcpspy.h
	$(CC) $(CFLAGS) -c $< -o $@

# Test target - build a simple test program
.PHONY: test
test: $(TARGET) test_program
	@echo "Running test program..."
	LD_PRELOAD=./$(TARGET) MCPSPY_ENABLE=1 ./test_program

test_program: test_program.c
	$(CC) -o test_program test_program.c

# Install target
.PHONY: install
install: $(TARGET)
	@echo "Installing MCPSpy userland library..."
	sudo cp $(TARGET) /usr/local/lib/
	sudo cp libmcpspy.h /usr/local/include/
	sudo ldconfig

# Uninstall target
.PHONY: uninstall
uninstall:
	@echo "Uninstalling MCPSpy userland library..."
	sudo rm -f /usr/local/lib/$(TARGET)
	sudo rm -f /usr/local/include/libmcpspy.h
	sudo ldconfig

# Clean target
.PHONY: clean
clean:
	@echo "Cleaning build artifacts..."
	rm -f $(OBJECTS) $(TARGET) test_program

# Debug target - build with debug symbols and no optimization
.PHONY: debug
debug: CFLAGS += -DDEBUG -g3 -O0
debug: clean $(TARGET)

# Show build configuration
.PHONY: config
config:
	@echo "Build Configuration:"
	@echo "  CC: $(CC)"
	@echo "  CFLAGS: $(CFLAGS)"
	@echo "  LDFLAGS: $(LDFLAGS)"
	@echo "  libpcap available: $(PCAP_AVAILABLE)"
	@echo "  OpenSSL available: $(SSL_AVAILABLE)"
	@echo "  Target: $(TARGET)"

# Help target
.PHONY: help
help:
	@echo "MCPSpy Userland Library Makefile"
	@echo ""
	@echo "Targets:"
	@echo "  all        - Build the shared library (default)"
	@echo "  test       - Build and run test program"
	@echo "  install    - Install library system-wide"
	@echo "  uninstall  - Remove installed library"
	@echo "  clean      - Remove build artifacts"
	@echo "  debug      - Build with debug symbols"
	@echo "  config     - Show build configuration"
	@echo "  help       - Show this help message"
	@echo ""
	@echo "Usage example:"
	@echo "  LD_PRELOAD=./libmcpspy.so your_mcp_program"
	@echo ""
	@echo "Environment variables:"
	@echo "  MCPSPY_ENABLE=1           - Auto-enable monitoring"
	@echo "  MCPSPY_LOG_FILE=file.log  - Log to file"
	@echo "  MCPSPY_MONITOR_STDIO=1    - Monitor stdio (default: 1)"
	@echo "  MCPSPY_MONITOR_HTTP=1     - Monitor HTTP (default: 1)"
	@echo "  MCPSPY_MONITOR_HTTPS=1    - Monitor HTTPS (default: 1)"
	@echo "  MCPSPY_MONITOR_PACKETS=1  - Monitor packets (default: 0)"

.DEFAULT_GOAL := all