# Contributing Guidelines

Thank you for your interest in contributing to Everything You Need! This document provides guidelines and instructions for contributing to the project.

## 🎯 Ways to Contribute

- **Add new price sources** - Extend support for more price data providers
- **Improve notification channels** - Add new notification methods (Discord, Slack, etc.)
- **Bug fixes** - Fix issues and improve reliability
- **Documentation** - Improve guides, API docs, and examples
- **Performance optimizations** - Make the application faster and more efficient
- **Tests** - Add test coverage and improve quality assurance

## 🚀 Getting Started

### Prerequisites
- Go 1.24.5 or later
- Git
- Text editor or IDE

### Development Setup

1. **Fork and Clone**
   ```bash
   git clone https://github.com/your-username/everything-you-need.git
   cd everything-you-need
   ```

2. **Install Dependencies**
   ```bash
   go mod tidy
   ```

3. **Run Tests**
   ```bash
   go test ./...
   ```

4. **Run the Application**
   ```bash
   go run main.go
   ```

## 📝 Development Workflow

### 1. Create a Feature Branch
```bash
git checkout -b feature/your-feature-name
# or
git checkout -b bugfix/issue-description
```

### 2. Make Your Changes
- Follow the coding standards (see below)
- Write tests for new functionality
- Update documentation as needed

### 3. Test Your Changes
```bash
# Run all tests
go test ./...

# Run with coverage
go test -cover ./...

# Run specific tests
go test ./services/price-tracker -v
```

### 4. Commit Your Changes
```bash
git add .
git commit -m "feat: add new price source for crypto exchanges"

# Or for bug fixes:
git commit -m "fix: handle network timeout errors in Doji source"
```

### 5. Push and Create PR
```bash
git push origin feature/your-feature-name
```

Then create a Pull Request through GitHub.

## 📋 Coding Standards

### Go Style Guidelines
- Follow [Go Code Review Comments](https://go.dev/wiki/CodeReviewComments)
- Use `gofmt` to format your code
- Use meaningful variable and function names
- Write clear comments for public APIs

### Project Structure
```
everything-you-need/
├── services/           # Service implementations
│   ├── price-tracker/  # Price tracking service
│   └── tele-noti/      # Telegram notification service
├── docs/               # Documentation
├── main.go             # Application entry point
└── README.md           # Project overview
```

### Package Organization
- Each service lives in its own package under `services/`
- Interfaces are defined in the main service file
- Implementations go in separate files (e.g., `doji.source.go`)
- Tests go in `*_test.go` files

### Naming Conventions

#### Files
- Service files: `service-name.service.go`
- Source implementations: `source-name.source.go`
- Factory files: `factory.go`
- Test files: `*_test.go`

#### Types and Functions
```go
// Interfaces: descriptive names ending with interface purpose
type PriceSource interface {}
type NotificationService interface {}

// Structs: descriptive names
type DojiSource struct {}
type TeleNotiService struct {}

// Functions: verb-noun pattern
func GetPrices() []Price {}
func SendNotification() error {}

// Constructors: NewTypeName pattern
func NewPriceManager() *PriceManager {}
```

### Error Handling
- Always handle errors explicitly
- Use wrapped errors for context: `fmt.Errorf("operation failed: %w", err)`
- Return meaningful error messages
- Don't ignore errors (avoid `_` unless absolutely necessary)

```go
// Good
prices, err := source.GetPrices()
if err != nil {
    return nil, fmt.Errorf("failed to fetch prices from %s: %w", source.GetSourceName(), err)
}

// Bad
prices, _ := source.GetPrices()
```

## 🧪 Testing Guidelines

### Test Structure
```go
func TestFunctionName_Scenario(t *testing.T) {
    // Arrange
    input := "test-input"
    expected := "expected-output"
    
    // Act
    result, err := FunctionName(input)
    
    // Assert
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    
    if result != expected {
        t.Errorf("expected %v, got %v", expected, result)
    }
}
```

### Test Categories

#### Unit Tests
- Test individual functions and methods
- Use mocks for external dependencies
- Fast execution (< 100ms per test)

```go
func TestPriceService_AddSource(t *testing.T) {
    service := NewPriceService()
    mockSource := &MockPriceSource{name: "test"}
    
    service.AddSource(mockSource)
    
    if len(service.sources) != 1 {
        t.Error("source was not added")
    }
}
```

#### Integration Tests
- Test interactions between components
- Use real APIs with test keys/sandbox environments
- Mark with build tags: `//go:build integration`

```go
//go:build integration

func TestDojiSource_RealAPI(t *testing.T) {
    if testing.Short() {
        t.Skip("skipping integration test")
    }
    
    source := NewDojiSource("test-api-key")
    prices, err := source.GetPrices()
    
    if err != nil {
        t.Fatalf("integration test failed: %v", err)
    }
    
    if len(prices) == 0 {
        t.Error("expected prices from real API")
    }
}
```

### Running Tests
```bash
# All tests
go test ./...

# Unit tests only
go test -short ./...

# Integration tests only
go test -tags=integration ./...

# With coverage
go test -cover ./...

# Specific package
go test ./services/price-tracker -v
```

## 📚 Documentation Standards

### Code Documentation
- All public functions and types must have comments
- Comments should explain **what** and **why**, not just **how**
- Use complete sentences

```go
// PriceSource defines the contract for all price data sources.
// Implementations should handle network errors gracefully and
// return standardized Price structures.
type PriceSource interface {
    // GetPrices fetches current price data from the source.
    // Returns empty slice (not nil) if no prices are available.
    GetPrices() ([]Price, error)
    
    // GetSourceName returns a unique identifier for this source.
    // This name is used for configuration and logging.
    GetSourceName() string
}
```

### README Updates
When adding new features:
- Update the feature list
- Add usage examples
- Update supported sources/services list

### API Documentation
- Update `docs/api-reference.md` for new interfaces
- Add examples for new functionality
- Document configuration options

## 🔧 Adding New Price Sources

Follow the detailed guide in [Adding New Sources](adding-sources.md):

1. Implement `PriceSource` interface
2. Register in factory
3. Add comprehensive tests
4. Update documentation
5. Provide usage examples

## 📨 Adding New Notification Services

Similar to price sources, but implement notification interfaces:

1. Define service interface (if new type)
2. Implement the service
3. Add factory support (if applicable)
4. Write tests
5. Document the service

Example structure:
```go
type NotificationService interface {
    SendMessage(message string) error
    SendFormattedMessage(message string, format MessageFormat) error
}

type DiscordService struct {
    webhookURL string
    client     *http.Client
}

func (d *DiscordService) SendMessage(message string) error {
    // Implementation
}
```

## 🐛 Bug Reports

When reporting bugs:
1. **Check existing issues** first
2. **Use the bug report template**
3. **Provide minimal reproduction steps**
4. **Include relevant logs/error messages**
5. **Specify environment details** (Go version, OS, etc.)

### Bug Report Template
```markdown
## Bug Description
Brief description of the bug

## Steps to Reproduce
1. Step one
2. Step two
3. Step three

## Expected Behavior
What should have happened

## Actual Behavior
What actually happened

## Environment
- Go version: 1.21.0
- OS: macOS 14.0
- Package version: v1.0.0

## Additional Context
Any other relevant information
```

## 💡 Feature Requests

For feature requests:
1. **Check existing requests** first
2. **Describe the problem** you're trying to solve
3. **Propose a solution** (optional but helpful)
4. **Consider backward compatibility**

## 🔍 Code Review Process

### For Contributors
- Keep PRs focused and small
- Write clear commit messages
- Respond to feedback promptly
- Update documentation as needed

### For Reviewers
- Be constructive and helpful
- Focus on code quality and maintainability
- Check for security issues
- Verify tests are adequate

### PR Checklist
- [ ] ✅ Code follows project style guidelines
- [ ] ✅ Tests pass locally
- [ ] ✅ New tests cover added functionality
- [ ] ✅ Documentation is updated
- [ ] ✅ No security vulnerabilities introduced
- [ ] ✅ Backward compatibility maintained
- [ ] ✅ Performance impact considered

## 🏷 Commit Message Format

Use conventional commits format:
```
type(scope): brief description

Optional longer description explaining the change.

Fixes #123
```

### Types
- `feat`: New feature
- `fix`: Bug fix
- `docs`: Documentation changes
- `style`: Code formatting (no logic changes)
- `refactor`: Code refactoring
- `test`: Adding or updating tests
- `chore`: Maintenance tasks

### Examples
```bash
git commit -m "feat(price-tracker): add Binance price source"
git commit -m "fix(telegram): handle rate limiting errors"
git commit -m "docs(api): update price source interface documentation"
git commit -m "test(doji): add integration tests for error scenarios"
```

## 🚀 Release Process

### Version Numbering
- Follow [Semantic Versioning](https://semver.org/)
- `MAJOR.MINOR.PATCH`
- Breaking changes increment MAJOR
- New features increment MINOR  
- Bug fixes increment PATCH

### Release Checklist
- [ ] All tests pass
- [ ] Documentation updated
- [ ] CHANGELOG.md updated
- [ ] Version tagged in git
- [ ] Release notes written

## 📞 Getting Help

- **Documentation**: Check `docs/` directory first
- **Issues**: Search existing GitHub issues
- **Discussions**: Use GitHub Discussions for questions
- **Email**: Contact maintainers for security issues

## 🙏 Recognition

Contributors are recognized in:
- README.md contributors section
- Release notes
- Git commit history

Thank you for contributing to Everything You Need! 🎉