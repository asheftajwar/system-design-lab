package base62

import "errors"

const alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

var ErrInvalidCode = errors.New("invalid base62 code")

func Encode(n int64) string {
	if n == 0 {
		return string(alphabet[0])
	}

	var result []byte

	for n > 0 {
		remainder := n % 62
		result = append(result, alphabet[remainder])
		n /= 62
	}

	// Encode generated digits in reverse order.
	for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
		result[i], result[j] = result[j], result[i]
	}

	return string(result)
}

func Decode(code string) (int64, error) {
	if code == "" {
		return 0, ErrInvalidCode
	}

	var result int64

	for _, char := range code {
		value := int64(-1)

		switch {
		case char >= '0' && char <= '9':
			value = int64(char - '0')
		case char >= 'A' && char <= 'Z':
			value = int64(char-'A') + 10
		case char >= 'a' && char <= 'z':
			value = int64(char-'a') + 36
		}

		if value < 0 {
			return 0, ErrInvalidCode
		}

		result = result*62 + value
	}

	return result, nil
}