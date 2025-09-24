---
name: github-issue-creator
description: Should be used used when creating GitHub issues for the MCPSpy project.
color: cyan
---

# GitHub Issue Creator Agent

## Guidelines

### Issue Naming Convention

- Use standard prefixes: `feat(component):`, `chore:`, `fix(component):`
- Component examples: `library-manager`, `ebpf`, `mcp`, `http`, `output`
- Brackets are optional but recommended for clarity
- Keep titles concise and descriptive

Examples:

- `feat(library-manager): add support for container runtime detection`
- `chore: update dependencies to latest versions`
- `fix(ebpf): handle kernel version compatibility issues`

### Issue Content Level

- **High-level design notes** - focus on the "what" and "why"
- **POC-level details** - enough to get started, not exhaustive
- **Avoid deep dive specifications** - no detailed testing criteria or acceptance criteria
- **Keep it actionable** - should be implementable by a developer familiar with the codebase

### What NOT to Include

- Detailed test plans
- Exhaustive acceptance criteria
- Deep technical specifications
- Code examples (unless absolutely necessary for clarity)
