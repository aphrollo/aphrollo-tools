package git

import (
	"bufio"
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ErrNoObject is what ReadBlob says of an object it cannot read itself and
// git cannot either.
var ErrNoObject = errors.New("git: no such object")

// sha1Len is the byte length of a SHA-1 object name. A repository of the
// SHA-256 format is read by git, never by this reader.
const sha1Len = 20

// Pack object types, as the pack format numbers them.
const (
	packCommit   = 1
	packTree     = 2
	packBlob     = 3
	packTag      = 4
	packOfsDelta = 6
	packRefDelta = 7
)

// maxDeltaResult bounds the size a delta header may claim for its result.
const maxDeltaResult = 1 << 31

// maxDeltaDepth bounds a delta chain, which git itself caps well below this.
const maxDeltaDepth = 4096

// ReadBlob answers the content of the blob oid names, exactly the bytes
// `git cat-file blob <oid>` prints. A loose object and an object in a pack are
// read from the object store's files, with no spawn; whatever this reader does
// not take on (a SHA-256 repository, an alternate object store, a partial
// clone's missing object) is asked of git, as one counted spawn.
func (c *Client) ReadBlob(oid string) ([]byte, error) {
	if len(oid) == 2*sha1Len && isObjectID(oid) {
		if kind, data, ok := c.readObject(oid); ok {
			if kind != packBlob {
				return nil, fmt.Errorf("git: object %s is not a blob", oid)
			}
			return data, nil
		}
	}
	out, err := c.Output("cat-file", "blob", oid)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrNoObject, oid)
	}
	return []byte(out), nil
}

// readObject reads one object out of the object store's files: its pack type
// and its content. ok is false when this reader cannot answer, whether the
// object is not in the files it reads or the files are in a shape it does not
// know.
func (c *Client) readObject(oid string) (kind int, data []byte, ok bool) {
	if kind, data, ok = c.readLoose(oid); ok {
		return kind, data, true
	}
	return c.readPacked(oid)
}

// looseKinds maps the type word of a loose object's header to its pack type.
var looseKinds = map[string]int{"commit": packCommit, "tree": packTree, "blob": packBlob, "tag": packTag}

// readLoose reads the zlib file `objects/<2 hex>/<38 hex>`.
func (c *Client) readLoose(oid string) (int, []byte, bool) {
	f, err := os.Open(filepath.Join(c.commonDir, "objects", oid[:2], oid[2:]))
	if err != nil {
		return 0, nil, false
	}
	defer f.Close()
	zr, err := zlib.NewReader(f)
	if err != nil {
		return 0, nil, false
	}
	defer zr.Close()
	raw, err := io.ReadAll(zr)
	if err != nil {
		return 0, nil, false
	}
	header, body, found := bytes.Cut(raw, []byte{0})
	if !found {
		return 0, nil, false
	}
	name, size, found := strings.Cut(string(header), " ")
	if !found {
		return 0, nil, false
	}
	if n, err := strconv.Atoi(size); err != nil || n != len(body) {
		return 0, nil, false
	}
	kind, known := looseKinds[name]
	return kind, body, known
}

// readPacked looks oid up in every pack index of the object store and reads
// it from the first pack that holds it.
func (c *Client) readPacked(oid string) (int, []byte, bool) {
	want, err := hexBytes(oid)
	if err != nil {
		return 0, nil, false
	}
	dir := filepath.Join(c.commonDir, "objects", "pack")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, nil, false
	}
	for _, e := range entries {
		if name := e.Name(); !strings.HasPrefix(name, "pack-") || !strings.HasSuffix(name, ".idx") {
			continue
		}
		idx := filepath.Join(dir, e.Name())
		off, found := packOffset(idx, want)
		if !found {
			continue
		}
		pack, err := os.Open(strings.TrimSuffix(idx, ".idx") + ".pack")
		if err != nil {
			return 0, nil, false
		}
		defer pack.Close()
		return c.readPackEntry(pack, off, 0)
	}
	return 0, nil, false
}

// hexBytes decodes a lower-case hex object name.
func hexBytes(s string) ([]byte, error) {
	out := make([]byte, len(s)/2)
	for i := range out {
		v, err := strconv.ParseUint(s[2*i:2*i+2], 16, 8)
		if err != nil {
			return nil, err
		}
		out[i] = byte(v)
	}
	return out, nil
}

// idxMagic opens a version 2 pack index.
var idxMagic = []byte{0xff, 't', 'O', 'c', 0, 0, 0, 2}

const (
	idxFanoutAt  = 8
	idxFanoutLen = 256 * 4
	idxTableAt   = idxFanoutAt + idxFanoutLen
)

// packOffset is where want starts in the pack the version 2 index at path
// describes: found is false for an object the index does not list and for an
// index of another shape.
func packOffset(path string, want []byte) (offset int64, found bool) {
	f, err := os.Open(path)
	if err != nil {
		return 0, false
	}
	defer f.Close()
	head := make([]byte, idxTableAt)
	if _, err := f.ReadAt(head, 0); err != nil || !bytes.Equal(head[:len(idxMagic)], idxMagic) {
		return 0, false
	}
	fanout := func(i int) int64 { return int64(binary.BigEndian.Uint32(head[idxFanoutAt+4*i:])) }
	total := fanout(255)
	lo, hi := int64(0), fanout(int(want[0]))
	if want[0] > 0 {
		lo = fanout(int(want[0]) - 1)
	}
	name := make([]byte, sha1Len)
	for lo < hi {
		mid := (lo + hi) / 2
		if _, err := f.ReadAt(name, idxTableAt+mid*sha1Len); err != nil {
			return 0, false
		}
		switch order := bytes.Compare(name, want); {
		case order == 0:
			return idxOffset(f, total, mid)
		case order < 0:
			lo = mid + 1
		default:
			hi = mid
		}
	}
	return 0, false
}

// idxOffset reads the pack offset of the object at position n of an index that
// lists total objects: the 4-byte table, whose high bit sends a large offset to
// the 8-byte table after it.
func idxOffset(f *os.File, total, n int64) (int64, bool) {
	small := make([]byte, 4)
	smallAt := idxTableAt + total*sha1Len + total*4 + n*4
	if _, err := f.ReadAt(small, smallAt); err != nil {
		return 0, false
	}
	v := binary.BigEndian.Uint32(small)
	if v&0x80000000 == 0 {
		return int64(v), true
	}
	big := make([]byte, 8)
	bigAt := idxTableAt + total*sha1Len + total*4 + total*4 + int64(v&0x7fffffff)*8
	if _, err := f.ReadAt(big, bigAt); err != nil {
		return 0, false
	}
	return int64(binary.BigEndian.Uint64(big)), true
}

// readPackEntry reads the object that starts at offset in pack, resolving a
// delta against its base, which sits in the same pack (an offset delta) or
// anywhere in the object store (a reference delta).
func (c *Client) readPackEntry(pack *os.File, offset int64, depth int) (int, []byte, bool) {
	if depth > maxDeltaDepth {
		return 0, nil, false
	}
	r := bufio.NewReader(io.NewSectionReader(pack, offset, 1<<62))
	kind, size, err := readEntryHeader(r)
	if err != nil {
		return 0, nil, false
	}
	switch kind {
	case packCommit, packTree, packBlob, packTag:
		data, ok := inflate(r, size)
		return kind, data, ok
	case packOfsDelta:
		back, err := readOffsetBack(r)
		if err != nil {
			return 0, nil, false
		}
		delta, ok := inflate(r, size)
		if !ok {
			return 0, nil, false
		}
		baseKind, base, ok := c.readPackEntry(pack, offset-back, depth+1)
		if !ok {
			return 0, nil, false
		}
		out, ok := applyDelta(base, delta)
		return baseKind, out, ok
	case packRefDelta:
		name := make([]byte, sha1Len)
		if _, err := io.ReadFull(r, name); err != nil {
			return 0, nil, false
		}
		delta, ok := inflate(r, size)
		if !ok {
			return 0, nil, false
		}
		baseKind, base, ok := c.readObject(fmt.Sprintf("%x", name))
		if !ok {
			return 0, nil, false
		}
		out, ok := applyDelta(base, delta)
		return baseKind, out, ok
	}
	return 0, nil, false
}

// readEntryHeader reads a pack entry's type and inflated size: three bits of
// type and four of size in the first byte, then seven more bits of size per
// byte while the high bit says there are more.
func readEntryHeader(r io.ByteReader) (kind int, size int64, err error) {
	b, err := r.ReadByte()
	if err != nil {
		return 0, 0, err
	}
	kind = int(b >> 4 & 7)
	size = int64(b & 15)
	for shift := uint(4); b&0x80 != 0; shift += 7 {
		if b, err = r.ReadByte(); err != nil {
			return 0, 0, err
		}
		size |= int64(b&0x7f) << shift
	}
	return kind, size, nil
}

// readOffsetBack reads an offset delta's distance back to its base: seven bits
// per byte, high bit for more, with one added at each continuation.
func readOffsetBack(r io.ByteReader) (int64, error) {
	b, err := r.ReadByte()
	if err != nil {
		return 0, err
	}
	back := int64(b & 0x7f)
	// walk-terminates: each turn reads one byte of the entry, and the read fails at its end
	for b&0x80 != 0 {
		if b, err = r.ReadByte(); err != nil {
			return 0, err
		}
		back = (back+1)<<7 | int64(b&0x7f)
	}
	return back, nil
}

// inflate reads the zlib stream r holds, which must be exactly size bytes
// long.
func inflate(r io.Reader, size int64) ([]byte, bool) {
	zr, err := zlib.NewReader(r)
	if err != nil {
		return nil, false
	}
	defer zr.Close()
	if size < 0 {
		return nil, false
	}
	// The size is a claim of the entry's header, not a fact: read what the
	// stream holds, one byte past the claim, and compare, so a corrupt header
	// cannot ask for memory the stream does not fill.
	out, err := io.ReadAll(io.LimitReader(zr, size+1))
	if err != nil || int64(len(out)) != size {
		return nil, false
	}
	return out, true
}

// applyDelta rebuilds an object from its base and a delta: the base's size and
// the result's size as varints, then instructions that copy a range of the
// base (high bit set; the low bits say which of the offset and size bytes
// follow) or insert the literal bytes after the instruction (1 to 127 of them).
func applyDelta(base, delta []byte) ([]byte, bool) {
	baseSize, n := deltaSize(delta)
	if n == 0 || baseSize != len(base) {
		return nil, false
	}
	delta = delta[n:]
	resultSize, n := deltaSize(delta)
	if n == 0 {
		return nil, false
	}
	delta = delta[n:]
	if resultSize < 0 || resultSize > maxDeltaResult {
		return nil, false
	}
	out := make([]byte, 0, min(resultSize, len(delta)+len(base)))
	// walk-terminates: each turn consumes at least the instruction byte of delta
	for len(delta) > 0 {
		op := delta[0]
		delta = delta[1:]
		if op&0x80 == 0 {
			if op == 0 || int(op) > len(delta) {
				return nil, false
			}
			if len(out)+int(op) > resultSize {
				return nil, false
			}
			out = append(out, delta[:op]...)
			delta = delta[op:]
			continue
		}
		var off, size int
		for i := range 4 {
			if op&(1<<i) != 0 {
				if len(delta) == 0 {
					return nil, false
				}
				off |= int(delta[0]) << (8 * i)
				delta = delta[1:]
			}
		}
		for i := range 3 {
			if op&(1<<(4+i)) != 0 {
				if len(delta) == 0 {
					return nil, false
				}
				size |= int(delta[0]) << (8 * i)
				delta = delta[1:]
			}
		}
		if size == 0 {
			size = 0x10000
		}
		if off+size > len(base) {
			return nil, false
		}
		if len(out)+size > resultSize {
			return nil, false
		}
		out = append(out, base[off:off+size]...)
	}
	return out, len(out) == resultSize
}

// deltaSize reads one little-endian base-128 size from the front of b and says
// how many bytes it took, 0 when b ends inside it.
func deltaSize(b []byte) (size, used int) {
	for i, c := range b {
		size |= int(c&0x7f) << (7 * i)
		if c&0x80 == 0 {
			return size, i + 1
		}
	}
	return 0, 0
}
