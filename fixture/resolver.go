package fixture

import (
	"context"
	"embed"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/viant/afs"
	afsembed "github.com/viant/afs/embed"
	"github.com/viant/afs/storage"
	"github.com/viant/structology"
)

const MaxSourceBytes int64 = 64 << 20

type resolver struct {
	fs           fs.FS
	root         string
	local        bool
	service      afs.Service
	options      []storage.Option
	embedManager storage.Manager
	embedFS      *embed.FS
	err          error
}

func Directory(root string) Resolver {
	r := &resolver{root: root, local: true}
	if !filepath.IsAbs(root) {
		r.err = fmt.Errorf("fixture directory must be absolute: %q", root)
	}
	return r
}
func FS(fsys fs.FS, root string) Resolver {
	r := &resolver{fs: fsys, root: root}
	if fsys == nil {
		r.err = fmt.Errorf("fixture filesystem is nil")
	}
	if root == "" {
		r.root = "."
	}
	if !fs.ValidPath(r.root) {
		r.err = fmt.Errorf("invalid fixture root %q", root)
	}
	return r
}

// Embed uses the AFS embed manager directly, retaining the filesystem on every
// read. It never falls back to an on-disk file with the same name.
func Embed(fsys *embed.FS, root string) Resolver {
	r := &resolver{embedFS: fsys, root: root}
	if fsys == nil {
		r.err = fmt.Errorf("embedded filesystem is nil")
		return r
	}
	if root == "" {
		r.root = "."
	}
	if !fs.ValidPath(r.root) {
		r.err = fmt.Errorf("invalid fixture root %q", root)
		return r
	}
	r.embedManager = afsembed.New(fsys)
	return r
}
func AFS(service afs.Service, baseURL string, options ...storage.Option) Resolver {
	r := &resolver{service: service, root: baseURL, options: append([]storage.Option(nil), options...)}
	if service == nil {
		r.err = fmt.Errorf("fixture AFS service is nil")
	}
	return r
}

func (r *resolver) Load(ctx context.Context, source Source) (structology.Maps, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	kind, err := source.kind()
	if err != nil {
		return nil, err
	}
	if kind == "maps" {
		return Clone(source.Data)
	}
	if kind == "typed" {
		var rows structology.Maps
		if err := rows.Store(source.Value); err != nil {
			return nil, fmt.Errorf("typed fixture: %w", err)
		}
		return rows, nil
	}
	content, err := r.Read(ctx, source)
	if err != nil {
		return nil, err
	}
	format := strings.ToLower(source.Format)
	if format == "" {
		name := source.Path
		if source.URL != "" {
			u, e := url.Parse(source.URL)
			if e != nil {
				return nil, e
			}
			name = u.Path
		}
		format = strings.TrimPrefix(strings.ToLower(path.Ext(name)), ".")
	}
	rows, err := decode(content, format, source.NullToken)
	if err != nil {
		return nil, fmt.Errorf("fixture %q: %w", sourceLabel(source), err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}
func sourceLabel(s Source) string {
	if s.Path != "" {
		return s.Path
	}
	if s.URL != "" {
		return s.URL
	}
	return s.Kind
}
func (r *resolver) Read(ctx context.Context, source Source) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	kind, err := source.kind()
	if err != nil {
		return nil, err
	}
	if kind == "bytes" {
		if int64(len(source.Content)) > MaxSourceBytes {
			return nil, fmt.Errorf("fixture exceeds %d bytes", MaxSourceBytes)
		}
		return append([]byte(nil), source.Content...), nil
	}
	if r.err != nil {
		return nil, r.err
	}
	var reader io.ReadCloser
	switch kind {
	case "file":
		if !fs.ValidPath(source.Path) || source.Path == "." || strings.Contains(source.Path, "\\") {
			return nil, fmt.Errorf("invalid fixture path %q", source.Path)
		}
		switch {
		case r.embedManager != nil:
			reader, err = r.embedManager.OpenURL(ctx, "embed:///"+path.Join(r.root, source.Path), r.embedFS)
		case r.fs != nil:
			reader, err = r.fs.Open(path.Join(r.root, source.Path))
		case r.local:
			var root, filename string
			root, err = filepath.EvalSymlinks(r.root)
			if err == nil {
				filename, err = filepath.EvalSymlinks(filepath.Join(root, filepath.FromSlash(source.Path)))
			}
			if err == nil {
				rel, e := filepath.Rel(root, filename)
				if e != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
					err = fmt.Errorf("fixture path escapes declared root")
				}
			}
			if err == nil {
				reader, err = os.Open(filename)
			}
		case r.service != nil:
			if r.root == "" {
				return nil, fmt.Errorf("file fixture requires an explicit AFS root")
			}
			var u *url.URL
			u, err = url.Parse(r.root)
			if err == nil && u.Scheme == "" && !filepath.IsAbs(r.root) {
				return nil, fmt.Errorf("AFS root must be an absolute path or URL")
			}
			if err == nil {
				u.Path = path.Join(u.Path, source.Path)
				reader, err = r.service.OpenURL(ctx, u.String(), r.options...)
			}
		default:
			return nil, fmt.Errorf("fixture %q requires an explicit resolver root", source.Path)
		}
	case "url":
		if r.service == nil {
			return nil, fmt.Errorf("URL fixture requires an explicitly configured AFS resolver")
		}
		reader, err = r.service.OpenURL(ctx, source.URL, r.options...)
	default:
		return nil, fmt.Errorf("fixture kind %q does not contain raw bytes", kind)
	}
	if err != nil {
		return nil, fmt.Errorf("open fixture %q: %w", sourceLabel(source), err)
	}
	defer reader.Close()
	data, err := io.ReadAll(io.LimitReader(reader, MaxSourceBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read fixture %q: %w", sourceLabel(source), err)
	}
	if int64(len(data)) > MaxSourceBytes {
		return nil, fmt.Errorf("fixture %q exceeds %d bytes", sourceLabel(source), MaxSourceBytes)
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	return data, nil
}
