package fnv

import (
	"errors"
	"hash"
	"testing"
)

type testHasher struct {
	n        int
	writeErr error
	sum64    uint64
}

func (h *testHasher) Write(p []byte) (n int, err error) { return h.n, h.writeErr }
func (h *testHasher) Sum64() uint64                     { return h.sum64 }
func (h *testHasher) Sum(b []byte) []byte               { return nil }
func (h *testHasher) Reset()                            {}
func (h *testHasher) Size() int                         { return 0 }
func (h *testHasher) BlockSize() int                    { return 0 }

func TestHashText(t *testing.T) {
	for _, tc := range []struct {
		name          string
		h             hash.Hash64
		text          string
		want, wantErr string
	}{
		{
			name: "write success",
			h: &testHasher{
				writeErr: nil,
				sum64:    123,
			},
			text: "foo",
			want: "3r",
		},
		{
			name: "write err",
			h: &testHasher{
				writeErr: errors.New("write error"),
			},
			text:    "foo",
			wantErr: "hashing failed: write error",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := hashText(tc.h, tc.text)
			if got != tc.want {
				t.Errorf("want hashed text %q, got %q", tc.want, got)
			}

			gotErr := ""
			if err != nil {
				gotErr = err.Error()
			}

			if gotErr != tc.wantErr {
				t.Errorf("want err %q, got %q", tc.wantErr, gotErr)
			}
		})
	}
}
