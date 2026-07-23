package index

import (
	"fmt"
	"os"
	"path/filepath"
)

// RootResolver is an immutable request-scoped resolver. It canonicalizes the
// library root once and can then validate multiple entries without repeatedly
// resolving that root.
type RootResolver struct {
	root string
}

func NewRootResolver(musicDir string) (*RootResolver, error) {
	root, err := filepath.Abs(musicDir)
	if err != nil {
		return nil, fmt.Errorf("resolve music root: %w", err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, fmt.Errorf("resolve music root: %w", err)
	}
	return &RootResolver{root: root}, nil
}

func (r *RootResolver) Root() string { return r.root }

// Resolve performs containment checks both before and after symlink
// evaluation. The caller is responsible for checking the resulting file type.
func (r *RootResolver) Resolve(mediaPath string) (string, error) {
	candidate := mediaPath
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(r.root, candidate)
	}
	candidate, err := filepath.Abs(candidate)
	if err != nil {
		return "", fmt.Errorf("resolve media path: %w", err)
	}
	if !withinRoot(r.root, candidate) {
		return "", ErrPathOutsideRoot
	}
	candidate, err = filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", fmt.Errorf("resolve media path: %w", err)
	}
	if !withinRoot(r.root, candidate) {
		return "", ErrPathOutsideRoot
	}
	return candidate, nil
}

func (r *RootResolver) Stat(mediaPath string) (string, os.FileInfo, error) {
	resolved, err := r.Resolve(mediaPath)
	if err != nil {
		return "", nil, err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", nil, err
	}
	return resolved, info, nil
}
