#include "libmcpspy.h"

// Stdio monitoring state
static int stdio_initialized = 0;

// Initialize stdio transport monitoring
int stdio_monitor_init(void) {
    if (stdio_initialized) {
        return 0; // Already initialized
    }

    // Stdio monitoring is handled by the main LD_PRELOAD hooks
    // No additional initialization needed for basic stdio monitoring
    
    stdio_initialized = 1;
    return 0;
}

// Cleanup stdio monitoring
void stdio_monitor_cleanup(void) {
    if (!stdio_initialized) {
        return;
    }
    
    stdio_initialized = 0;
}