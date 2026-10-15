package net

import (
	"errors"
	"io"
	"strings"
	"testing"
)

type rangeTestReadCloser struct {
	io.Reader
	closeErr error
	closed   bool
}

func (r *rangeTestReadCloser) Close() error {
	r.closed = true
	return r.closeErr
}

func TestGetRangedHttpReader(t *testing.T) {
	closeErr := errors.New("close failed")
	for _, test := range []struct {
		name       string
		offset     int64
		length     int64
		readLength int
		want       string
		wantError  bool
	}{
		{name: "limited", offset: 2, length: 3, want: "234"},
		{name: "unlimited", offset: 2, length: -1, want: "2345", wantError: true},
		{name: "empty", length: 0, want: ""},
		{name: "short source", offset: 2, length: 8, want: "2345", wantError: true},
		{name: "closed early", offset: 2, length: 3, readLength: 1, want: "2", wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := &rangeTestReadCloser{Reader: strings.NewReader("012345"), closeErr: closeErr}
			reader, err := GetRangedHttpReader(source, test.offset, test.length)
			if err != nil {
				t.Fatal(err)
			}
			var content []byte
			if test.readLength > 0 {
				content = make([]byte, test.readLength)
				_, err = io.ReadFull(reader, content)
			} else {
				content, err = io.ReadAll(reader)
			}
			if err != nil {
				t.Fatal(err)
			}
			if string(content) != test.want {
				t.Fatalf("content = %q, want %q", content, test.want)
			}
			err = reader.Close()
			if test.wantError && !errors.Is(err, closeErr) {
				t.Fatalf("Close() = %v, want %v", err, closeErr)
			}
			if !test.wantError && err != nil {
				t.Fatalf("Close() = %v, want nil", err)
			}
			if !source.closed {
				t.Fatal("underlying reader was not closed")
			}
		})
	}
}

type rangeTestErrorReader struct{ err error }

func (r rangeTestErrorReader) Read([]byte) (int, error) { return 0, r.err }

func TestGetRangedHttpReaderReturnsSkipError(t *testing.T) {
	readErr := errors.New("read failed")
	source := &rangeTestReadCloser{Reader: rangeTestErrorReader{err: readErr}}
	reader, err := GetRangedHttpReader(source, 1, 3)
	if reader != nil || !errors.Is(err, readErr) {
		t.Fatalf("GetRangedHttpReader() = %v, %v, want nil, %v", reader, err, readErr)
	}
}
