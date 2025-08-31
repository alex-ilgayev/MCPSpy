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
kubectl apply -f https://raw.githubusercontent.com/alex-ilgayev/mcpspy/v0.0.3/deploy/kubernetes/mcpspy.yaml
```

This creates:
- A dedicated `mcpspy` namespace
- Required RBAC permissions
- A DaemonSet that runs on all nodes

## Real-World Example: Monitoring LangFlow in Kubernetes

This example demonstrates how to deploy LangFlow with MCPSpy to monitor its MCP communications.

### 1. Deploy the Langflow development environment on Kubernetes

The [Langflow integrated development environment (IDE) Helm chart](https://github.com/langflow-ai/langflow-helm-charts/tree/main/charts/langflow-ide) is designed to provide a complete environment for developers to create, test, and debug their flows. It includes both the Langflow API and visual editor.

#### Prerequisites
- A [Kubernetes](https://kubernetes.io/docs/setup/) cluster
- [kubectl](https://kubernetes.io/docs/tasks/tools/#kubectl)
- [Helm](https://helm.sh/docs/intro/install/)

#### Prepare a minikube Cluster

This example uses [Minikube](https://minikube.sigs.k8s.io/docs/start/), but you can use any Kubernetes cluster.

1. Start Minikube with sufficient resources:

```bash
minikube start --cpus=4 --memory=8192
```

#### Install the Langflow IDE Helm chart

1. Add the repository to Helm and then update it:

    ```bash
    helm repo add langflow https://langflow-ai.github.io/langflow-helm-charts
    helm repo update
    ```

2. Install Langflow with the default options in the langflow namespace:

    ```bash
    helm install langflow-ide langflow/langflow-ide -n langflow --create-namespace
    ```

3. Check the status of the pods:

    ```bash
    kubectl get pods -n langflow
    ```

### 2. Deploy MCPSpy

```bash
kubectl apply -f https://raw.githubusercontent.com/alex-ilgayev/mcpspy/v0.0.3/deploy/kubernetes/mcpspy.yaml
```

### 3. Access the Langflow IDE

Enable local port forwarding to access Langflow from your local machine:

1. Make the Langflow API accessible from your local machine at port 7860:

   ```bash
   kubectl port-forward -n langflow svc/langflow-service-backend 7860:7860
   ```

2. Make the visual editor accessible from your local machine at port 8080:

    ```bash
    kubectl port-forward -n langflow svc/langflow-service 8080:8080
    ```

Now you can do the following:

- Access the Langflow API at http://localhost:7860.
- Access the Langflow visual editor at http://localhost:8080.

### 4. Create a Flow that Uses MCP

1. Open the Langflow visual editor at http://localhost:8080

2. Create a new flow that uses an MCP Tools (MCP Server) and an Agent (LLM). For example:
   - Add an MCP Server node (e.g., Time MCP)
   - Add an LLM node (e.g., OpenAI GPT-5 or any other)

   Here you can find an example of how to add Time MCP Server:
   - [Time MCP Server](https://mcp.so/server/time/modelcontextprotocol)
   - [Model Context Protocol servers](https://github.com/modelcontextprotocol/servers/tree/main/src/time)

3. Run the flow by pressing the Playground button in the top right corner.

4. Ask a question that will trigger the MCP interaction, such as "What time is it?"

### 5. Observe MCP Traffic

View the MCPSpy logs to see the MCP traffic:

```bash
# Get the MCPSpy pod on the node where LangFlow is running
LANGFLOW_NODE=$(kubectl -n langflow get pod -l app=langflow-service -o jsonpath='{.items[0].spec.nodeName}')
MCPSPY_POD=$(kubectl -n mcpspy get pods -o jsonpath="{.items[?(@.spec.nodeName=='$LANGFLOW_NODE')].metadata.name}")

# View the MCPSpy output
kubectl -n mcpspy exec -it $MCPSPY_POD -- cat /output/mcpspy.jsonl

# View the MCPSpy logs
kubectl -n mcpspy logs $MCPSPY_POD -f
```

## Troubleshooting

### bpftool

Deploy bpftool POD to inspect if eBPF programs were loaded.

```bash
kubectl run -it --rm bpftool \
    --image=gyutaeb/bpftool:v7.5.0 \
    --restart=Never \
    --privileged \
    --command -- sh
```

Inside the pod, run:

```bash
bpftool prog show
bpftool map show
exit
```

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
