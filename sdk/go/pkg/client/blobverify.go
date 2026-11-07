package client

// A blob's address IS the SHA-256 of its bytes, so a reader can prove what it received.

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"strings"
)

// BlobIntegrityError: the bytes a blob read delivered do not hash to the blob's address
// (truncated, corrupted, or served for the wrong hash). It is returned at the END of
// the stream, after the bytes were delivered, so a streaming consumer must treat what
// it already wrote as invalid. Never cache such bytes.
type BlobIntegrityError struct {
	Hash string // the address that was requested
	Got  string // the SHA-256 of what actually arrived
	Size int64  // bytes received
}

func (e *BlobIntegrityError) Error() string {
	return fmt.Sprintf("blob %s: the %d bytes received hash to %s, not to the blob's address (truncated or corrupted)", e.Hash, e.Size, e.Got)
}

// IsBlobIntegrityError reports whether err is (or wraps) a *BlobIntegrityError.
func IsBlobIntegrityError(err error) bool {
	var e *BlobIntegrityError
	return errors.As(err, &e)
}

// VerifyBlob wraps body so that reading it to the end checks it against hash: the final
// Read returns a *BlobIntegrityError instead of io.EOF on a mismatch. Bytes pass through
// unchanged; closing early checks nothing. A hash that is not a SHA-256 address (64 hex
// digits) cannot be checked and returns body as it is.
func VerifyBlob(hash string, body io.ReadCloser) io.ReadCloser {
	if !isBlobAddress(hash) {
		return body
	}
	return &verifyingBody{want: strings.ToLower(hash), body: body, sum: sha256.New()}
}

func isBlobAddress(s string) bool {
	if len(s) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

type verifyingBody struct {
	want string
	body io.ReadCloser
	sum  hash.Hash
	size int64
	err  error // the sticky end-of-stream verdict
}

func (v *verifyingBody) Read(p []byte) (int, error) {
	if v.err != nil {
		return 0, v.err
	}
	n, err := v.body.Read(p)
	if n > 0 {
		v.sum.Write(p[:n])
		v.size += int64(n)
	}
	if err == io.EOF {
		if got := hex.EncodeToString(v.sum.Sum(nil)); got != v.want {
			err = &BlobIntegrityError{Hash: v.want, Got: got, Size: v.size}
		}
		v.err = err
	}
	return n, err
}

func (v *verifyingBody) Close() error { return v.body.Close() }
