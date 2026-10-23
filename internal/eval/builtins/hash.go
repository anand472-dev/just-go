package builtins

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"gitea.com/wimwian/just-go/internal/sys"
)

// sha256Hex is the SHA-256 digest of s, as lowercase hex. It is pure.
func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// sha256File is the SHA-256 digest of the file at path, relative to the
// working directory, as lowercase hex. It is impure: it reads Host.FS.
func sha256File(h Host, args []string) (string, error) {
	path := resolve(h.WorkDir, args[0])
	data, err := h.FS.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("failed to open `%s`: %s", path, sys.ErrorText(err))
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// blake3Hex is the BLAKE3 digest of s, as lowercase hex. It is pure.
func blake3Hex(s string) string {
	sum := blake3Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

// blake3File is the BLAKE3 digest of the file at path, relative to the working
// directory, as lowercase hex. It is impure: it reads Host.FS.
func blake3File(h Host, args []string) (string, error) {
	path := resolve(h.WorkDir, args[0])
	data, err := h.FS.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("failed to hash `%s`: %s", path, sys.ErrorText(err))
	}
	sum := blake3Sum(data)
	return hex.EncodeToString(sum[:]), nil
}

// uuidV4 is a random version 4 UUID in the form upstream prints it: lowercase
// hex in five groups. Its bytes come from Host.Random, and it is impure
// because of that.
func uuidV4(h Host, _ []string) (string, error) {
	var b [16]byte
	if _, err := h.Random.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}
