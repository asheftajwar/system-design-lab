package base62

import (
	"errors"
	"testing"
)

func TestEncode(t *testing.T) {
	tests := []struct {
		name string
		id   int64
		want string
	}{
		{
			name: "zero",
			id:   0,
			want: "0",
		},
		{
			name: "one",
			id:   1,
			want: "1",
		},
		{
			name: "single digit boundary",
			id:   61,
			want: "z",
		},
		{
			name: "first two digit value",
			id:   62,
			want: "10",
		},
		{
			name: "known value",
			id:   1234567,
			want: "5BAN",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Encode(tt.id)

			if got != tt.want {
				t.Fatalf("Encode(%d) = %q, want %q", tt.id, got, tt.want)
			}
		})
	}
}

func TestDecode(t *testing.T) {
	tests := []struct {
		name string
		code string
		want int64
	}{
		{
			name: "zero",
			code: "0",
			want: 0,
		},
		{
			name: "one",
			code: "1",
			want: 1,
		},
		{
			name: "single digit boundary",
			code: "z",
			want: 61,
		},
		{
			name: "first two digit value",
			code: "10",
			want: 62,
		},
		{
			name: "known value",
			code: "5BAN",
			want: 1234567,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Decode(tt.code)

			if err != nil {
				t.Fatalf("Decode(%q) returned error: %v", tt.code, err)
			}

			if got != tt.want {
				t.Fatalf("Decode(%q) = %d, want %d", tt.code, got, tt.want)
			}
		})
	}
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	values := []int64{
		0,
		1,
		61,
		62,
		123,
		1234567,
		100000000,
		9223372036854775807,
	}

	for _, value := range values {
		code := Encode(value)

		got, err := Decode(code)
		if err != nil {
			t.Fatalf(
				"Decode(Encode(%d)) returned error: %v",
				value,
				err,
			)
		}

		if got != value {
			t.Fatalf(
				"round trip failed: value=%d code=%q decoded=%d",
				value,
				code,
				got,
			)
		}
	}
}

func TestDecodeInvalidCode(t *testing.T) {
	tests := []string{
		"",
		"-1",
		"hello!",
		"abc_123",
		"abc-123",
	}

	for _, code := range tests {
		t.Run(code, func(t *testing.T) {
			_, err := Decode(code)

			if err != ErrInvalidCode {
				t.Fatalf(
					"Decode(%q) error = %v, want %v",
					code,
					err,
					ErrInvalidCode,
				)
			}
		})
	}
}

func TestDecodeOverflow(t *testing.T) {
	_, err := Decode("AzL8n0Y58m8")
	if !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("Decode() error = %v, want %v", err, ErrInvalidCode)
	}
}
