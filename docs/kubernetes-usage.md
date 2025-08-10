# MCPSpy in Kubernetes: Monitoring AI/LLM Services

This guide explains how to deploy and use MCPSpy in a Kubernetes environment to monitor Model Context Protocol (MCP) communications between AI/LLM services.

## Why Use MCPSpy in Kubernetes?

Modern AI/LLM applications often run in Kubernetes clusters, with components like:

- **LangFlow/LangGraph** deployments for workflow orchestration
- **AI model servers** (local or remote)
- **Custom applications** that use MCP for AI integration

MCPSpy helps you:

1. **Debug AI integrations** by monitoring MCP traffic in real-time
2. **Audit AI interactions** for security and compliance
3. **Optimize performance** by identifying bottlenecks in AI service calls
4. **Detect data leakage** by inspecting what's sent to AI services

## Deployment

### Cluster-Wide Monitoring (DaemonSet)

Deploy MCPSpy as a DaemonSet to monitor all nodes in your cluster:

```bash
kubectl apply -f https://raw.githubusercontent.com/alex-ilgayev/mcpspy/v0.0.2/manifests/mcpspy.yaml
```

This creates:
- A dedicated `mcpspy` namespace
- Required RBAC permissions
- A DaemonSet that runs on all nodes

## Real-World Example: Monitoring LangFlow in minikube

This example demonstrates how to deploy LangFlow with MCPSpy to monitor its MCP communications.

### 1. Deploy LangFlow

TBA

### 2. Deploy MCPSpy

```bash
kubectl apply -f https://raw.githubusercontent.com/alex-ilgayev/mcpspy/v0.0.2/manifests/mcpspy.yaml
```

### 3. Configure LangFlow with an MCP Server

Access the LangFlow UI and configure it to use an MCP-compatible LLM service:

1. Port-forward the LangFlow service:
   ```bash
   kubectl -n ai-services port-forward svc/langflow 7860:7860
   ```

2. Open http://localhost:7860 in your browser

3. Create a new flow with an LLM component (e.g., OpenAI, Anthropic)

4. Configure the LLM with your API key

### 4. Observe MCP Traffic

View the MCPSpy logs to see the MCP traffic:

```bash
# Get the MCPSpy pod on the node where LangFlow is running
LANGFLOW_NODE=$(kubectl -n ai-services get pod -l app=langflow -o jsonpath='{.items[0].spec.nodeName}')
MCPSPY_POD=$(kubectl -n mcpspy get pod -l app.kubernetes.io/name=mcpspy -o name | grep $LANGFLOW_NODE | head -n 1)

# View the MCPSpy logs
kubectl -n mcpspy exec -it $MCPSPY_POD -- cat /output/mcpspy.jsonl
```

You should see MCP messages showing:
- Tool registration requests
- LLM completion requests
- Response handling

## Troubleshooting

### bpftool

TBA , bpftool POD to inspect if eBPF programs were loaded.

### No MCP Traffic Detected

1. Verify MCPSpy is running with privileged access:
   ```bash
   kubectl -n mcpspy get pods
   ```

2. Check if your application is using MCP:
   - Not all AI integrations use MCP
   - Some may use HTTP transport which is not yet supported by MCPSpy

### Performance Considerations

- MCPSpy uses eBPF which has minimal overhead
- For production clusters, consider:
  - Using resource limits in the deployment
  - Targeting specific nodes where AI services run
  - Rotating log files to prevent disk space issues

## Security Considerations

- MCPSpy requires privileged access to use eBPF
- Consider the security implications in production environments
- Use RBAC to limit who can access the MCPSpy pods and logs
- Be aware that MCPSpy can see sensitive data in MCP messages

## Conclusion

MCPSpy provides valuable insights into MCP communications in Kubernetes environments. By deploying it alongside your AI/LLM services, you can gain visibility into how these services interact, helping with debugging, optimization, and security auditing.
