package token

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testShareID = "e0123456-7890-abcd-ef01-234567890abc"

func TestGenerate(t *testing.T) {
	svc := NewTokenService()

	tok, err := svc.Generate(testShareID)
	require.NoError(t, err)

	parts := strings.Split(tok, ".")
	require.Len(t, parts, tokenParts)
	assert.Equal(t, tokenVersion, parts[0])
	assert.Equal(t, hash(testShareID), parts[1])
	assert.NotEmpty(t, parts[2])
}

func TestGenerateDeterminism(t *testing.T) {
	svc := NewTokenService()

	tok1, err := svc.Generate(testShareID)
	require.NoError(t, err)
	tok2, err := svc.Generate(testShareID)
	require.NoError(t, err)

	assert.Equal(t, hash(testShareID), strings.Split(tok1, ".")[1])
	assert.Equal(t, hash(testShareID), strings.Split(tok2, ".")[1])
	assert.NotEqual(t, tok1, tok2)

	other, err := svc.Generate("9f9f9f9-9f9f-9f9f-9f9f-9f9f9f9f9f9f")
	require.NoError(t, err)
	assert.NotEqual(t, strings.Split(tok1, ".")[1], strings.Split(other, ".")[1])
}

func TestVerify(t *testing.T) {
	svc := NewTokenService()

	tok, err := svc.Generate(testShareID)
	require.NoError(t, err)

	hashPart := strings.Split(tok, ".")[1]
	secretPart := strings.Split(tok, ".")[2]
	storedSecretHash := hash(secretPart)

	tests := []struct {
		name             string
		token            string
		storedSecretHash string
		expectError      bool
	}{
		{
			name:             "valid token",
			token:            tok,
			storedSecretHash: storedSecretHash,
		},
		{
			name:             "tampered secret",
			token:            "v1." + hashPart + ".tampered",
			storedSecretHash: storedSecretHash,
			expectError:      true,
		},
		{
			name:             "wrong stored secret",
			token:            tok,
			storedSecretHash: hash("other-secret"),
			expectError:      true,
		},
		{
			name:             "wrong version",
			token:            "v2." + hashPart + "." + secretPart,
			storedSecretHash: storedSecretHash,
			expectError:      true,
		},
		{
			name:             "missing version",
			token:            hashPart + "." + secretPart,
			storedSecretHash: storedSecretHash,
			expectError:      true,
		},
		{
			name:             "too many parts",
			token:            "v1." + hashPart + "." + secretPart + ".extra",
			storedSecretHash: storedSecretHash,
			expectError:      true,
		},
		{
			name:             "empty hash",
			token:            "v1.." + secretPart,
			storedSecretHash: storedSecretHash,
			expectError:      true,
		},
		{
			name:             "empty secret",
			token:            "v1." + hashPart + ".",
			storedSecretHash: storedSecretHash,
			expectError:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := svc.Verify(tt.token, tt.storedSecretHash)
			if tt.expectError {
				assert.ErrorIs(t, err, ErrInvalidToken)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
