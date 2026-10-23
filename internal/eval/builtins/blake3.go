package builtins

import "math/bits"

// BLAKE3, as its specification defines the plain hash of one input (no key,
// no key derivation). It is ported here, not imported, so that the module
// takes no dependency for one builtin. It is pure. The digests are checked
// against the oracle over the chunk and tree edges in hash_test.go.

const (
	blake3ChunkLen = 1024
	blake3BlockLen = 64

	flagChunkStart = 1 << 0
	flagChunkEnd   = 1 << 1
	flagParent     = 1 << 2
	flagRoot       = 1 << 3
)

// blake3IV is the initial chaining value, the same as SHA-256's.
var blake3IV = [8]uint32{
	0x6A09E667, 0xBB67AE85, 0x3C6EF372, 0xA54FF53A,
	0x510E527F, 0x9B05688C, 0x1F83D9AB, 0x5BE0CD19,
}

// blake3Perm is the message permutation applied after each round.
var blake3Perm = [16]int{2, 6, 3, 10, 7, 0, 4, 13, 1, 11, 12, 5, 9, 14, 15, 8}

// blake3Sum returns the 32-byte BLAKE3 digest of data.
func blake3Sum(data []byte) [32]byte {
	var out [16]uint32
	if len(data) <= blake3ChunkLen {
		out = chunkNode(data, 0, true)
	} else {
		left, right := splitTree(data)
		l := subtreeCV(left, 0)
		r := subtreeCV(right, uint64(len(left)/blake3ChunkLen))
		out = compress(blake3IV, parentWords(l, r), 0, blake3BlockLen, flagParent|flagRoot)
	}
	var sum [32]byte
	for i := range 8 {
		putWordLE(sum[4*i:], out[i])
	}
	return sum
}

// subtreeCV is the chaining value of the subtree of data whose first chunk is
// chunk number counter. Only a root node is output with the root flag.
func subtreeCV(data []byte, counter uint64) [8]uint32 {
	if len(data) <= blake3ChunkLen {
		out := chunkNode(data, counter, false)
		return [8]uint32(out[:8])
	}
	left, right := splitTree(data)
	l := subtreeCV(left, counter)
	r := subtreeCV(right, counter+uint64(len(left)/blake3ChunkLen))
	out := compress(blake3IV, parentWords(l, r), 0, blake3BlockLen, flagParent)
	return [8]uint32(out[:8])
}

// splitTree divides data, which spans more than one chunk, at the left
// subtree's end: the largest power of two of chunks that is less than all of
// them. Every chunk on the left is full.
func splitTree(data []byte) (left, right []byte) {
	chunks := (len(data) + blake3ChunkLen - 1) / blake3ChunkLen
	cut := (1 << (bits.Len(uint(chunks-1)) - 1)) * blake3ChunkLen
	return data[:cut], data[cut:]
}

// parentWords is the block a parent node compresses: the two chaining values.
func parentWords(l, r [8]uint32) [16]uint32 {
	var m [16]uint32
	copy(m[:8], l[:])
	copy(m[8:], r[:])
	return m
}

// chunkNode compresses one chunk, which is at most 1024 bytes, and returns the
// final state of its last block. With root set, that block carries the root
// flag, so the first eight words of the result are the digest. An empty chunk
// is one empty block.
func chunkNode(chunk []byte, counter uint64, root bool) [16]uint32 {
	cv := blake3IV
	blocks := max((len(chunk)+blake3BlockLen-1)/blake3BlockLen, 1)
	var out [16]uint32
	for b := range blocks {
		start := b * blake3BlockLen
		end := min(start+blake3BlockLen, len(chunk))
		flags := uint32(0)
		if b == 0 {
			flags |= flagChunkStart
		}
		if b == blocks-1 {
			flags |= flagChunkEnd
			if root {
				flags |= flagRoot
			}
		}
		out = compress(cv, blockWords(chunk[start:end]), counter, uint32(end-start), flags)
		cv = [8]uint32(out[:8])
	}
	return out
}

// blockWords reads up to 64 bytes as sixteen little-endian words, with zeros
// after the bytes given.
func blockWords(b []byte) [16]uint32 {
	var block [blake3BlockLen]byte
	copy(block[:], b)
	var m [16]uint32
	for i := range m {
		m[i] = wordLE(block[4*i:])
	}
	return m
}

// compress is the BLAKE3 compression function: seven rounds over the state,
// then the feed-forward. Its result is the sixteen words of the state.
func compress(cv [8]uint32, m [16]uint32, counter uint64, blockLen, flags uint32) [16]uint32 {
	s := [16]uint32{
		cv[0], cv[1], cv[2], cv[3], cv[4], cv[5], cv[6], cv[7],
		blake3IV[0], blake3IV[1], blake3IV[2], blake3IV[3],
		uint32(counter), uint32(counter >> 32), blockLen, flags,
	}
	for range 7 {
		blake3Round(&s, m)
		var p [16]uint32
		for i, j := range blake3Perm {
			p[i] = m[j]
		}
		m = p
	}
	for i := range 8 {
		s[i] ^= s[i+8]
		s[i+8] ^= cv[i]
	}
	return s
}

// blake3Round mixes the state's columns and then its diagonals.
func blake3Round(s *[16]uint32, m [16]uint32) {
	blake3G(s, 0, 4, 8, 12, m[0], m[1])
	blake3G(s, 1, 5, 9, 13, m[2], m[3])
	blake3G(s, 2, 6, 10, 14, m[4], m[5])
	blake3G(s, 3, 7, 11, 15, m[6], m[7])
	blake3G(s, 0, 5, 10, 15, m[8], m[9])
	blake3G(s, 1, 6, 11, 12, m[10], m[11])
	blake3G(s, 2, 7, 8, 13, m[12], m[13])
	blake3G(s, 3, 4, 9, 14, m[14], m[15])
}

// blake3G is the mixing function over four state words and two message words.
func blake3G(s *[16]uint32, a, b, c, d int, mx, my uint32) {
	s[a] = s[a] + s[b] + mx
	s[d] = bits.RotateLeft32(s[d]^s[a], -16)
	s[c] = s[c] + s[d]
	s[b] = bits.RotateLeft32(s[b]^s[c], -12)
	s[a] = s[a] + s[b] + my
	s[d] = bits.RotateLeft32(s[d]^s[a], -8)
	s[c] = s[c] + s[d]
	s[b] = bits.RotateLeft32(s[b]^s[c], -7)
}

// wordLE reads the four bytes at the start of b as a little-endian word.
func wordLE(b []byte) uint32 {
	return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
}

// putWordLE writes w to the four bytes at the start of b, little-endian.
func putWordLE(b []byte, w uint32) {
	b[0], b[1], b[2], b[3] = byte(w), byte(w>>8), byte(w>>16), byte(w>>24)
}
