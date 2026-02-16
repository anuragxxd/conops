# Contributing to ConOps

Thank you for your interest in contributing to ConOps! We welcome contributions from the community.

## How to Contribute

### Reporting Bugs

If you find a bug, please open an issue with:
- A clear description of the problem
- Steps to reproduce
- Expected vs actual behavior
- Your environment (OS, Docker version, ConOps version)
- Relevant logs or error messages

### Suggesting Features

Feature requests are welcome! Please open an issue describing:
- The problem you're trying to solve
- Your proposed solution
- Any alternatives you've considered
- Use cases and examples

### Pull Requests

1. **Fork the repository** and create a new branch from `master`
2. **Make your changes** with clear, descriptive commits
3. **Test your changes** - ensure existing tests pass and add new tests if needed
4. **Update documentation** - if you're changing behavior or adding features
5. **Submit a pull request** with a clear description of what you've changed and why

#### Development Setup

```bash
# Clone your fork
git clone https://github.com/YOUR-USERNAME/conops.git
cd conops

# Run the controller locally
go run ./cmd/conops

# Run tests
go test ./...

# Build
go build -o bin/conops ./cmd/conops
```

#### Code Style

- Follow standard Go conventions (`gofmt`, `go vet`)
- Write clear, self-documenting code with comments where needed
- Keep functions focused and testable
- Use meaningful variable and function names

#### Commit Messages

- Use present tense ("Add feature" not "Added feature")
- Be descriptive but concise
- Reference issues/PRs when relevant (`Fixes #123`)

### Areas We'd Love Help With

- **Documentation improvements** - clearer guides, more examples
- **Testing** - unit tests, integration tests, edge cases
- **Platform support** - testing on different OS/architectures
- **Features** - see open issues tagged with `enhancement`
- **Bug fixes** - see open issues tagged with `bug`

## Code of Conduct

Be respectful, constructive, and collaborative. We're all here to build something useful together.

## Questions?

Open an issue or start a discussion in [GitHub Discussions](https://github.com/anuragxxd/conops/discussions).

---

Thank you for contributing! 🚀
