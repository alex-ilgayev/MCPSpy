#include <stdio.h>
#include <string.h>
#include <unistd.h>

int main() {
    // Test JSON-RPC message that should be detected by MCPSpy
    const char* test_message = "{\"jsonrpc\":\"2.0\",\"method\":\"tools/call\",\"params\":{\"name\":\"get_weather\",\"arguments\":{\"city\":\"New York\"}},\"id\":1}\n";
    
    printf("MCPSpy Test Program\n");
    printf("===================\n");
    printf("This program will generate test MCP communications that should be detected by the MCPSpy library.\n\n");
    
    // Test stdio output (should be monitored)
    printf("Sending test MCP message to stdout:\n");
    write(STDOUT_FILENO, test_message, strlen(test_message));
    
    // Test stderr output
    fprintf(stderr, "Test MCP error message: %s", test_message);
    
    // Test stdin read (simulate reading MCP response)
    printf("\nTest completed. Check MCPSpy output for detected messages.\n");
    
    return 0;
}