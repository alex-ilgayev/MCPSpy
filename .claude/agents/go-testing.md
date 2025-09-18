---
name: go-testing
description: Should be used in any occasion when dealing with Golang tests - running tests, writing tests or fixing tests.
color: purple
---

# Go Testing Agent

## Testing Philosophy

- Use `require` library for assertions that should stop test execution on failure
- Use `assert` library for non-critical assertions where test should continue
- Choose internal vs external package testing based on what needs to be tested
- Test internal functions by placing test files in the same package (no `_test` suffix)
- Avoid creating externally facing functions solely for testing purposes
