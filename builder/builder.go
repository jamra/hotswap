// Package builder provides Go build orchestration.
package builder

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// DefaultOutput is the default output path for builds
const DefaultOutput = "./tmp/main"

// Options configures the builder
type Options struct {
	// Output is the path for the built binary
	Output string

	// BuildArgs are additional arguments to pass to go build
	BuildArgs []string

	// WorkDir is the working directory for the build
	WorkDir string

	// Env is additional environment variables for the build
	Env []string

	// Logger for build events
	Logger *log.Logger
}

func (o *Options) defaults() {
	if o.Output == "" {
		o.Output = DefaultOutput
	}
	if o.Logger == nil {
		o.Logger = log.Default()
	}
}

// Builder handles Go builds
type Builder struct {
	opts Options
}

// New creates a new builder
func New(opts Options) *Builder {
	opts.defaults()
	return &Builder{opts: opts}
}

// Build compiles the Go project
func (b *Builder) Build() error {
	// Ensure output directory exists
	outDir := filepath.Dir(b.opts.Output)
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	// Construct build command
	args := []string{"build", "-o", b.opts.Output}
	args = append(args, b.opts.BuildArgs...)

	b.opts.Logger.Printf("[builder] go %s", strings.Join(args, " "))

	cmd := exec.Command("go", args...)
	cmd.Dir = b.opts.WorkDir
	cmd.Env = append(os.Environ(), b.opts.Env...)

	// Capture output
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		// Include stderr in the error for build failures
		errOutput := stderr.String()
		if errOutput != "" {
			return fmt.Errorf("build failed:\n%s", errOutput)
		}
		return fmt.Errorf("build failed: %w", err)
	}

	if stdout.Len() > 0 {
		b.opts.Logger.Printf("[builder] %s", stdout.String())
	}

	b.opts.Logger.Printf("[builder] build successful: %s", b.opts.Output)
	return nil
}

// OutputPath returns the path to the built binary
func (b *Builder) OutputPath() string {
	return b.opts.Output
}

// Clean removes the built binary
func (b *Builder) Clean() error {
	return os.Remove(b.opts.Output)
}
