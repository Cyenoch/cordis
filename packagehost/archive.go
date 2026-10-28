package packagehost

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/schema"
)

// Limits bound compressed input and actual bytes read, not just archive headers.
type Limits struct {
	ArchiveBytes  int64
	FileBytes     int64
	ExpandedBytes int64
	Entries       int
}

func DefaultLimits() Limits { return Limits{64 << 20, 64 << 20, 256 << 20, 10000} }
func (l Limits) validate() error {
	if l.ArchiveBytes <= 0 || l.FileBytes <= 0 || l.ExpandedBytes <= 0 || l.FileBytes == int64(^uint64(0)>>1) || l.ExpandedBytes == int64(^uint64(0)>>1) || l.Entries <= 0 || l.Entries > 10000 {
		return fmt.Errorf("invalid package limits")
	}
	return nil
}
func validateConfigSchema(raw json.RawMessage) error {
	if len(raw) == 0 {
		return nil
	}
	return schema.Check(raw)
}
func validateConfig(raw, value json.RawMessage) error {
	if len(value) == 0 {
		value = json.RawMessage(`{}`)
	}
	if err := contract.CheckJSON(value, 32, 10000); err != nil {
		return err
	}
	if len(raw) == 0 {
		return nil
	}
	return schema.Validate(raw, value)
}

type byteBudget struct {
	remaining int64
	file      int64
}

func copyFile(ctx context.Context, dst string, src io.Reader, budget *byteBudget) (err error) {
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
		return fmt.Errorf("create package parent: %w", err)
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("create package file: %w", err)
	}
	defer func() { err = errors.Join(err, out.Close()) }()
	limit := min(budget.remaining, budget.file)
	reader := &contextReader{ctx: ctx, reader: src}
	n, err := io.Copy(out, io.LimitReader(reader, limit+1))
	if err != nil {
		return fmt.Errorf("copy package file: %w", err)
	}
	if n > limit {
		return fmt.Errorf("package expanded byte quota exceeded")
	}
	budget.remaining -= n
	return nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

func extractZIP(ctx context.Context, archive, dest string, l Limits) (err error) {
	if err = ctx.Err(); err != nil {
		return err
	}
	file, err := openRegular(nil, archive)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	st, err := file.Stat()
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() || st.Size() > l.ArchiveBytes {
		return fmt.Errorf("archive is not regular or exceeds %d bytes", l.ArchiveBytes)
	}
	if err = checkZIPDirectory(ctx, file, st.Size(), l.Entries); err != nil {
		return fmt.Errorf("read zip directory: %w", err)
	}
	zr, err := zip.NewReader(file, st.Size())
	if err != nil {
		return fmt.Errorf("read zip: %w", err)
	}
	if len(zr.File) > l.Entries {
		return fmt.Errorf("archive entry quota exceeded")
	}
	var files, dirs []string
	for _, z := range zr.File {
		name := strings.TrimSuffix(z.Name, "/")
		if z.Flags&1 != 0 || z.Mode()&os.ModeSymlink != 0 || !z.Mode().IsRegular() && !z.FileInfo().IsDir() {
			return fmt.Errorf("encrypted or linked/special zip entry %q", z.Name)
		}
		if z.FileInfo().IsDir() {
			dirs = append(dirs, name)
			continue
		}
		if z.UncompressedSize64 > uint64(l.FileBytes) {
			return fmt.Errorf("file %q exceeds quota", name)
		}
		files = append(files, name)
	}
	if err := checkDirectories(files, dirs); err != nil {
		return err
	}
	budget := byteBudget{remaining: l.ExpandedBytes, file: l.FileBytes}
	for _, z := range zr.File {
		if z.FileInfo().IsDir() {
			continue
		}
		r, e := z.Open()
		if e != nil {
			return fmt.Errorf("open zip entry %q: %w", z.Name, e)
		}
		e = copyFile(ctx, filepath.Join(dest, filepath.FromSlash(z.Name)), r, &budget)
		e = errors.Join(e, r.Close())
		if e != nil {
			return fmt.Errorf("extract %q: %w", z.Name, e)
		}
	}
	return ctx.Err()
}

func copyDirectory(ctx context.Context, source, dest string, l Limits) error {
	// os.Root confines every open, including races replacing intermediate directories.
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("source root must be a real directory")
	}
	root, err := os.OpenRoot(source)
	if err != nil {
		return fmt.Errorf("open source root: %w", err)
	}
	var names, dirs []string
	count := 0
	err = fs.WalkDir(root.FS(), ".", func(name string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if name == "." {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		count++
		if count > l.Entries {
			return fmt.Errorf("directory entry quota exceeded")
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("source link %q", name)
		}
		if d.IsDir() {
			dirs = append(dirs, name)
			return nil
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("source special file %q", name)
		}
		names = append(names, name)
		return nil
	})
	if err != nil {
		return errors.Join(err, root.Close())
	}
	if err = checkDirectories(names, dirs); err != nil {
		return errors.Join(err, root.Close())
	}
	budget := byteBudget{remaining: l.ExpandedBytes, file: l.FileBytes}
	for _, name := range names {
		if e := checkSourceParents(root, name); e != nil {
			return errors.Join(e, root.Close())
		}
		f, e := openRegular(root, name)
		if e != nil {
			return errors.Join(e, root.Close())
		}
		info, e := f.Stat()
		if e == nil {
			e = checkLinks(f, info)
		}
		if e == nil {
			e = copyFile(ctx, filepath.Join(dest, filepath.FromSlash(name)), f, &budget)
		}
		e = errors.Join(e, f.Close())
		if e != nil {
			return errors.Join(e, root.Close())
		}
	}
	return root.Close()
}

func stageDirectory(ctx context.Context, source, dest string, l Limits) error {
	if err := copyDirectory(ctx, source, dest, l); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(dest, IntegrityFile)); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return generateIntegrity(ctx, dest, l)
		}
		return err
	}
	return nil
}

func checkSourceParents(root *os.Root, name string) error {
	for dir := filepath.Dir(filepath.FromSlash(name)); dir != "."; dir = filepath.Dir(dir) {
		info, err := root.Lstat(dir)
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("source parent is linked or not a directory: %q", dir)
		}
	}
	return nil
}

func readMetadata(dir, name string) ([]byte, error) {
	f, err := openRegular(nil, filepath.Join(dir, name))
	if err != nil {
		return nil, err
	}
	data, readErr := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	err = errors.Join(readErr, f.Close())
	if err != nil {
		return nil, err
	}
	if len(data) > 1<<20 {
		return nil, fmt.Errorf("metadata %s exceeds 1 MiB", name)
	}
	return data, nil
}

func hashDirectory(ctx context.Context, dir string, l Limits) ([]IntegrityEntry, error) {
	var entries []IntegrityEntry
	var names, dirs []string
	remaining := l.ExpandedBytes
	countEntries := 0
	err := filepath.WalkDir(dir, func(full string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if full == dir {
			return nil
		}
		rel, err := filepath.Rel(dir, full)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(rel)
		countEntries++
		if countEntries > l.Entries {
			return fmt.Errorf("entry count quota exceeded")
		}
		if d.IsDir() {
			dirs = append(dirs, name)
			return nil
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("nonregular managed file %q", name)
		}
		names = append(names, name)
		f, err := openRegular(nil, full)
		if err != nil {
			return err
		}
		info, e := f.Stat()
		if e == nil {
			e = checkLinks(f, info)
		}
		if e != nil {
			return errors.Join(e, f.Close())
		}
		h := sha256.New()
		count, e := io.Copy(h, io.LimitReader(&contextReader{ctx, f}, min(l.FileBytes, remaining)+1))
		e = errors.Join(e, f.Close())
		if e != nil {
			return e
		}
		if count > l.FileBytes || count > remaining {
			return fmt.Errorf("expanded byte quota exceeded")
		}
		remaining -= count
		if name != IntegrityFile && name != SignatureFile {
			entries = append(entries, IntegrityEntry{name, uint64(count), hex.EncodeToString(h.Sum(nil))})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if err := checkDirectories(names, dirs); err != nil {
		return nil, err
	}
	slices.SortFunc(entries, func(a, b IntegrityEntry) int { return strings.Compare(a.Path, b.Path) })
	return entries, nil
}

func inspect(ctx context.Context, dir, source string, development bool, l Limits, decoder MetadataDecoder) (Package, []byte, *Signature, error) {
	p, input, err := inspectContent(ctx, dir, source, development, l, decoder)
	if err != nil {
		return p, nil, nil, err
	}
	sig, err := readSignature(dir)
	return p, input, sig, err
}

func inspectContent(ctx context.Context, dir, source string, development bool, l Limits, decoder MetadataDecoder) (Package, []byte, error) {
	var p Package
	raw, err := readMetadata(dir, ManifestFile)
	if err != nil {
		return p, nil, err
	}
	m, err := decodeMetadata(ctx, raw, os.DirFS(dir), decoder)
	if err != nil {
		return p, nil, err
	}
	table, err := readMetadata(dir, IntegrityFile)
	if err != nil {
		return p, nil, err
	}
	var integrity Integrity
	if err = contract.Decode(table, &integrity); err != nil {
		return p, nil, err
	}
	input, err := SignatureInput(integrity.Files)
	if err != nil {
		return p, nil, err
	}
	actual, err := hashDirectory(ctx, dir, l)
	if err != nil {
		return p, nil, err
	}
	expected, err := SignatureInput(actual)
	if err != nil {
		return p, nil, err
	}
	if !bytes.Equal(input, expected) {
		return p, nil, fmt.Errorf("package integrity mismatch")
	}
	listed := map[string]bool{}
	for _, entry := range actual {
		listed[entry.Path] = true
	}
	artifacts := []string{m.Runtime.Entry}
	if m.Runtime.Kind == "native" {
		artifacts = nil
		for _, a := range m.Runtime.Artifacts {
			artifacts = append(artifacts, a.Path)
		}
	}
	for _, a := range artifacts {
		if !listed[a] {
			return p, nil, fmt.Errorf("missing runtime artifact %q", a)
		}
	}
	digest := sha256.Sum256(input)
	p = Package{contract.PackageRef{ID: m.ID, Version: m.Version, Digest: hex.EncodeToString(digest[:])}, m, dir, source, development}
	return p, input, nil
}

func readSignature(dir string) (*Signature, error) {
	var sig *Signature
	raw, err := readMetadata(dir, SignatureFile)
	if err == nil {
		sig = &Signature{}
		if err = contract.Decode(raw, sig); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return sig, nil
}
func generateIntegrity(ctx context.Context, dir string, l Limits) error {
	entries, err := hashDirectory(ctx, dir, l)
	if err != nil {
		return err
	}
	data, err := json.Marshal(Integrity{entries})
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, IntegrityFile), data, 0600)
}

// Pack snapshots a developer directory and writes a deterministic unsigned ZIP.
func Pack(ctx context.Context, dir, archive string) (p Package, err error) {
	return PackWithDecoder(ctx, dir, archive, nil)
}

func PackWithDecoder(ctx context.Context, dir, archive string, decoder MetadataDecoder) (p Package, err error) {
	stage, err := os.MkdirTemp("", "cordis-pack-")
	if err != nil {
		return p, err
	}
	defer func() { err = errors.Join(err, os.RemoveAll(stage)) }()
	l := DefaultLimits()
	if err = copyDirectory(ctx, dir, stage, l); err != nil {
		return p, err
	}
	if decoder == nil {
		if err = stageAuthor(ctx, stage); err != nil {
			return p, err
		}
	}
	if err = os.Remove(filepath.Join(stage, SignatureFile)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return p, err
	}
	if err = generateIntegrity(ctx, stage, l); err != nil {
		return p, err
	}
	p, _, _, err = inspect(ctx, stage, dir, true, l, decoder)
	if err != nil {
		return p, err
	}
	if err = writeZIP(ctx, stage, archive, l); err != nil {
		return p, err
	}
	p.Directory = ""
	return p, nil
}

// Sign rewrites an existing archive atomically with an Ed25519 signature.
func Sign(ctx context.Context, archive, keyID string, key ed25519.PrivateKey) (p Package, err error) {
	return SignWithDecoder(ctx, archive, keyID, key, nil)
}

func SignWithDecoder(ctx context.Context, archive, keyID string, key ed25519.PrivateKey, decoder MetadataDecoder) (p Package, err error) {
	if keyID == "" || len(key) != ed25519.PrivateKeySize {
		return p, fmt.Errorf("invalid signing key")
	}
	stage, err := os.MkdirTemp(filepath.Dir(archive), ".cordis-sign-")
	if err != nil {
		return p, err
	}
	defer func() { err = errors.Join(err, os.RemoveAll(stage)) }()
	l := DefaultLimits()
	if err = extractZIP(ctx, archive, stage, l); err != nil {
		return p, err
	}
	p, input, _, err := inspect(ctx, stage, archive, false, l, decoder)
	if err != nil {
		return p, err
	}
	raw, err := json.Marshal(Signature{keyID, base64.StdEncoding.EncodeToString(ed25519.Sign(key, input))})
	if err != nil {
		return p, err
	}
	if err = os.WriteFile(filepath.Join(stage, SignatureFile), raw, 0600); err != nil {
		return p, err
	}
	if err = writeZIP(ctx, stage, archive, l); err != nil {
		return p, err
	}
	p.Directory = ""
	return p, nil
}

func writeZIP(ctx context.Context, dir, archive string, l Limits) (err error) {
	f, err := os.CreateTemp(filepath.Dir(archive), ".cordis-archive-")
	if err != nil {
		return err
	}
	name := f.Name()
	defer func() {
		if e := os.Remove(name); e != nil && !errors.Is(e, os.ErrNotExist) {
			err = errors.Join(err, e)
		}
	}()
	zw := zip.NewWriter(f)
	var names []string
	err = filepath.WalkDir(dir, func(full string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() {
			return nil
		}
		rel, e := filepath.Rel(dir, full)
		if e != nil {
			return e
		}
		names = append(names, filepath.ToSlash(rel))
		return nil
	})
	slices.Sort(names)
	for _, name := range names {
		if err != nil {
			break
		}
		if err = ctx.Err(); err != nil {
			break
		}
		h := &zip.FileHeader{Name: name, Method: zip.Deflate}
		h.SetMode(0600)
		var w io.Writer
		w, err = zw.CreateHeader(h)
		if err != nil {
			break
		}
		var r *os.File
		r, err = os.Open(filepath.Join(dir, filepath.FromSlash(name)))
		if err != nil {
			break
		}
		_, err = io.Copy(w, &contextReader{ctx, r})
		err = errors.Join(err, r.Close())
	}
	err = errors.Join(err, zw.Close())
	if err == nil {
		err = f.Sync()
	}
	info, statErr := f.Stat()
	err = errors.Join(err, statErr, f.Close())
	if err != nil {
		return err
	}
	if info.Size() > l.ArchiveBytes {
		return fmt.Errorf("result archive exceeds byte quota")
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = replaceFile(name, archive); err != nil {
		return err
	}
	return syncDir(filepath.Dir(archive))
}
