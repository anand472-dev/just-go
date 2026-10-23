package builtins

import (
	"strings"
	"testing"

	"gitea.com/wimwian/just-go/internal/sys/sysfake"
)

// hashPattern is the content the file vectors hash: byte i is (31*i + 7) mod 256.
// The vectors were read from the oracle over the same bytes.
func hashPattern(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte((31*i + 7) % 256)
	}
	return string(b)
}

// hashVectors are the file sizes that matter to BLAKE3 (the block and chunk
// edges, and sizes that make a tree of several chunks), with the digests the
// oracle gives for them. Each row is size, blake3 hex, sha256 hex.
var hashFileVectors = []struct {
	size           int
	blake3, sha256 string
}{
	{0, "af1349b9f5f9a1a6a0404dea36dcc9499bcb25c9adc112b7cc9a93cae41f3262", "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"},
	{1, "448bd8dd9624154a690f8e84dc52d6f633ba7cd545c4d3c9b4e0f6a2f6fa71f4", "ca358758f6d27e6cf45272937977a748fd88391db679ceda7dc7bf1f005ee879"},
	{63, "7a7ae5f9b6891d25edecc530fbf41f88f0e4035b8fa17715cfb1201ba3c913ae", "280ed3e8ff1df845b2e7dfe6ac6cee817bef20e783cc65abc41b818b4d2fe076"},
	{64, "580eedf630212f2a9bd14712f93921e1a2712117290a21a8974a029532e93b11", "c6ab9724ade5b6a7a1edfffb12f3aa9181351355af8fd08c919952ad211339dd"},
	{65, "7f55325c3368e44f79edddb7b1a079b8aeae7ab43a0254b3012e564d75c4c1ae", "788367c73c7ddf4c53f65e68cc0d943e6227ab55b0e78ba63ace822b1c6301c0"},
	{1023, "7cc12c8435bc5cdb011ba62b7367601fb7d30b23b32e177f7e41907b210a8673", "088dffd828fc6ba469156f35fa7d1243acb23b944afb1dd8270e9264d7a6bc22"},
	{1024, "16f3b22ae43940fb8c8f328b033272ae752c203c3385d00bdda1696540f4c37e", "8d7e566766f6bd1bb4cac87cadfde681197f9243f4d2692a0fd12674092212a7"},
	{1025, "b8c5c46b114817810a6ed499350cb4d2423cd23dd08d32c137b226d8559b8ab0", "15b5bbecf752ad00e85ff42843b5dce9df388bc38ab97cf06e528727f5937413"},
	{2048, "634f590a498b3e29165cd8bd32f30a99a2b0d8949a3d7ce35779b6253d8ca0d5", "7c7272c96bd53928d659650ce0d351531ccca5b7ce618d14f48ec5c8ffd4919f"},
	{2049, "3c6cc85bfe26acef94b6bb440e8256c3fc541be3afe18bfffcd2c30c38e8a633", "51fbf57c17dbc32661774ec470c1543256b03a7392b25c179380f62475ea1e5c"},
	{3072, "ef59bae05ee1055873dce05dfc322342deaf90b6e45b4329297027447b1996e6", "3dedbe25c5ff01c827287bf81fa49e3213aa4204184de9a924189fddc9197c31"},
	{4097, "3000e690809e62e93e015f60ad2710797d6b3524c780b3bbb941154616c8a2e2", "bb37fb3430fb3bc003f70512ee0189aec84b029b221885c8f16280d1b935483e"},
	{5000, "a3d69d42e4f8b44ec13499a8c2d7bec15bd61e33716dce27a725ae331dc18165", "1e92fd98f113aba0a78e0830ca06e2775912370feab112dfc57bf3258b810595"},
	{70000, "e80cb002a8dbde01fdb6b1f96894c058b3d6eadc4a86ec00454d0fde20b4a1a5", "3500f58cfd1bd88e231edf56dca995542a702bd54525804e5a8604c8aa5cb52e"},
}

// hashTextVectors are the digests of strings, with the oracle's results.
var hashTextVectors = []struct {
	text, blake3, sha256 string
}{
	{"", "af1349b9f5f9a1a6a0404dea36dcc9499bcb25c9adc112b7cc9a93cae41f3262", "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"},
	{"abc", "6437b3ac38465133ffb63b75273a8db548c558465d79db03fd359c6cd5bd9d85", "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"},
	{"héllo", "4e406513432b4da52ae947c084ea5cf33ef02406f837159d04eed2e86688e376", "3c48591d8d098a4538f5e013dfcf406e948eac4d3277b10bf614e295d6068179"},
	{"5943ee37-0000-1000-8000-010203040506", "026c9f740a793ff536ddf05f8915ea4179421f47f0fa9545476076e9ba8f3f2b", "2330d7f5eb94a820b54fed59a8eced236f80b633a504289c030b6a65aef58871"},
}

func TestSHA256AndBLAKE3OfStringsAgreeWithUpstream(t *testing.T) {
	for _, tc := range hashTextVectors {
		if got, err := call(t, "blake3", []string{tc.text}); err != nil || got != tc.blake3 {
			t.Errorf("blake3(%q) = %q, %v; want %q", tc.text, got, err, tc.blake3)
		}
		if got, err := call(t, "sha256", []string{tc.text}); err != nil || got != tc.sha256 {
			t.Errorf("sha256(%q) = %q, %v; want %q", tc.text, got, err, tc.sha256)
		}
	}
}

func TestBLAKE3OfFilesAgreesWithUpstreamAcrossChunkEdges(t *testing.T) {
	for _, tc := range hashFileVectors {
		h := host(sysfake.FakeFS{"/w/f": hashPattern(tc.size)})
		got, err := hostCall(t, h, "blake3_file", "f")
		if err != nil || got != tc.blake3 {
			t.Errorf("blake3_file(%d bytes) = %q, %v; want %q", tc.size, got, err, tc.blake3)
		}
	}
}

func TestSHA256OfFilesAgreesWithUpstream(t *testing.T) {
	for _, tc := range hashFileVectors {
		h := host(sysfake.FakeFS{"/w/f": hashPattern(tc.size)})
		got, err := hostCall(t, h, "sha256_file", "f")
		if err != nil || got != tc.sha256 {
			t.Errorf("sha256_file(%d bytes) = %q, %v; want %q", tc.size, got, err, tc.sha256)
		}
	}
}

func TestFileHashesFailWithUpstreamWording(t *testing.T) {
	h := host(sysfake.FakeFS{})
	_, err := hostCall(t, h, "blake3_file", "missing")
	if err == nil || !strings.HasPrefix(err.Error(), "failed to hash `/w/missing`: ") {
		t.Errorf("blake3_file(missing) error = %v", err)
	}
	_, err = hostCall(t, h, "sha256_file", "missing")
	if err == nil || !strings.HasPrefix(err.Error(), "failed to open `/w/missing`: ") {
		t.Errorf("sha256_file(missing) error = %v", err)
	}
}

func TestUUIDFormatsRandomBytesAsVersionFourRFC4122(t *testing.T) {
	// Bytes 0 to 15 become version 4 (byte 6) and the RFC 4122 variant (byte 8).
	h := Host{Random: &sysfake.FakeRandom{Bytes: []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}}}
	got, err := hostCall(t, h, "uuid")
	if err != nil || got != "00010203-0405-4607-8809-0a0b0c0d0e0f" {
		t.Errorf("uuid() = %q, %v", got, err)
	}
}

func TestUUIDFailsWhenTheRandomSourceFails(t *testing.T) {
	h := Host{Random: &sysfake.FakeRandom{}}
	if _, err := hostCall(t, h, "uuid"); err == nil {
		t.Error("uuid() with an empty random source: want an error")
	}
}

func BenchmarkBLAKE3(b *testing.B) {
	data := []byte(hashPattern(4096))
	b.SetBytes(int64(len(data)))
	for i := 0; i < b.N; i++ {
		blake3Sum(data)
	}
}
