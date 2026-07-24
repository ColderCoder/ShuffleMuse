package cover

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/ColderCoder/ShuffleMuse/internal/mediaexec"
	"github.com/ColderCoder/ShuffleMuse/internal/stream"
)

func TestDirectoryCoverPreferenceAndEmbeddedFallbackOrder(t *testing.T) {
	dir := t.TempDir()
	audioPath := writeTestFile(t, filepath.Join(dir, "track.flac"), []byte("audio"))
	jpegPath := filepath.Join(dir, "Cover.JPG")
	writeJPEG(t, jpegPath, 8, 6)
	writePNG(t, filepath.Join(dir, "cover.png"), 8, 6, false)

	loader := NewLoader(nil)
	descriptor, err := loader.Describe(context.Background(), audioPath)
	if err != nil {
		t.Fatal(err)
	}
	if descriptor.Kind != Fallback || descriptor.Source != "Cover.JPG" || descriptor.FilePath != jpegPath || descriptor.ContentType != "image/jpeg" {
		t.Fatalf("directory JPEG preference = %+v", descriptor)
	}

	embeddedDir := t.TempDir()
	embeddedAudio := writeTestFile(t, filepath.Join(embeddedDir, "embedded.flac"), []byte("audio"))
	embeddedLoader := NewLoader(nil)
	var calls []descriptorKind
	embeddedLoader.discover = func(_ context.Context, key descriptorKey) (Descriptor, error) {
		calls = append(calls, key.kind)
		if key.kind == directoryDescriptor {
			return Descriptor{}, ErrNotFound
		}
		return Descriptor{Kind: Embedded, ContentType: "image/jpeg", Name: "cover.jpg", RequiresRender: true}, nil
	}
	got, err := embeddedLoader.Describe(context.Background(), embeddedAudio)
	if err != nil || got.Kind != Embedded {
		t.Fatalf("embedded fallback = %+v/%v", got, err)
	}
	if len(calls) != 2 || calls[0] != directoryDescriptor || calls[1] != embeddedDescriptor {
		t.Fatalf("discovery order = %v", calls)
	}
}

func TestDirectoryCoverPrefersCoverNamesBeforeFolderNames(t *testing.T) {
	t.Run("folder JPEG before folder PNG", func(t *testing.T) {
		dir := t.TempDir()
		jpegPath := filepath.Join(dir, "Folder.JPG")
		writePNG(t, filepath.Join(dir, "folder.png"), 8, 6, false)
		writeJPEG(t, jpegPath, 8, 6)

		descriptor, err := discoverDirectoryCover(dir)
		if err != nil {
			t.Fatal(err)
		}
		if descriptor.Source != "Folder.JPG" || descriptor.FilePath != jpegPath || descriptor.ContentType != "image/jpeg" {
			t.Fatalf("folder JPEG preference = %+v", descriptor)
		}
	})

	t.Run("cover basename before folder basename", func(t *testing.T) {
		dir := t.TempDir()
		coverPath := filepath.Join(dir, "cover.png")
		writeJPEG(t, filepath.Join(dir, "folder.jpg"), 8, 6)
		writePNG(t, coverPath, 8, 6, false)

		descriptor, err := discoverDirectoryCover(dir)
		if err != nil {
			t.Fatal(err)
		}
		if descriptor.Source != "cover.png" || descriptor.FilePath != coverPath || descriptor.ContentType != "image/png" {
			t.Fatalf("cover basename preference = %+v", descriptor)
		}
	})
}

func TestInvalidDirectoryCoverDoesNotProbeEmbedded(t *testing.T) {
	dir := t.TempDir()
	audioPath := writeTestFile(t, filepath.Join(dir, "track.flac"), []byte("audio"))
	writeTestFile(t, filepath.Join(dir, "cover.jpg"), []byte("not an image"))
	loader := NewLoader(nil)
	embeddedCalls := 0
	originalDiscover := loader.discover
	loader.discover = func(ctx context.Context, key descriptorKey) (Descriptor, error) {
		if key.kind == embeddedDescriptor {
			embeddedCalls++
		}
		return originalDiscover(ctx, key)
	}
	if _, err := loader.Describe(context.Background(), audioPath); !errors.Is(err, ErrNotFound) {
		t.Fatalf("invalid external error = %v", err)
	}
	if embeddedCalls != 0 {
		t.Fatalf("invalid external cover triggered %d embedded probes", embeddedCalls)
	}
}

func TestDescriptorCacheNegativeTTLAndNoImageBytes(t *testing.T) {
	dir := t.TempDir()
	now := time.Unix(100, 0)
	loader := NewLoader(nil, Config{Entries: 4, Bytes: 1 << 20, NegativeTTL: time.Minute})
	loader.now = func() time.Time { return now }
	calls := 0
	loader.discover = func(context.Context, descriptorKey) (Descriptor, error) {
		calls++
		return Descriptor{}, ErrNotFound
	}
	for range 2 {
		if _, err := loader.DescribeDirectory(context.Background(), dir); !errors.Is(err, ErrNotFound) {
			t.Fatalf("negative describe = %v", err)
		}
	}
	if calls != 1 {
		t.Fatalf("negative descriptor calls = %d", calls)
	}
	now = now.Add(2 * time.Minute)
	if _, err := loader.DescribeDirectory(context.Background(), dir); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("negative cache did not expire: %d", calls)
	}
	descriptors, renders, cacheBytes := loader.CacheStats()
	if descriptors != 1 || renders != 0 || cacheBytes <= 0 || cacheBytes >= 1<<20 {
		t.Fatalf("cache stats = descriptors %d renders %d bytes %d", descriptors, renders, cacheBytes)
	}
}

func TestCompressionThresholdBoundariesUseORSemantics(t *testing.T) {
	tests := []struct {
		name       string
		width      int
		size       int64
		wantRender bool
	}{
		{name: "equal dimensions and bytes", width: 1536, size: 1 << 20, wantRender: false},
		{name: "one pixel over", width: 1537, size: 1 << 20, wantRender: true},
		{name: "one byte over", width: 1536, size: (1 << 20) + 1, wantRender: true},
		{name: "both below", width: 1200, size: 900_000, wantRender: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			coverPath := filepath.Join(dir, "cover.png")
			writePNGHeader(t, coverPath, test.width, 1)
			padFile(t, coverPath, test.size)
			descriptor, err := discoverDirectoryCover(dir)
			if err != nil {
				t.Fatal(err)
			}
			if descriptor.RequiresRender != test.wantRender {
				t.Fatalf("RequiresRender = %v, want %v (size=%d)", descriptor.RequiresRender, test.wantRender, descriptor.Size)
			}
			if test.wantRender && (descriptor.ContentType != "image/jpeg" || descriptor.ContentLength() != "") {
				t.Fatalf("rendered descriptor headers = %+v length=%q", descriptor, descriptor.ContentLength())
			}
			if !test.wantRender && descriptor.ContentType != "image/png" {
				t.Fatalf("unchanged PNG type = %q", descriptor.ContentType)
			}
		})
	}
}

func TestExternalCoverSafetyLimitsAndCorruption(t *testing.T) {
	t.Run("bytes", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "cover.png")
		writePNGHeader(t, path, 1, 1)
		padFile(t, path, maxCoverBytes+1)
		if _, err := discoverDirectoryCover(dir); !errors.Is(err, ErrNotFound) {
			t.Fatalf("oversized error = %v", err)
		}
	})
	t.Run("single edge", func(t *testing.T) {
		dir := t.TempDir()
		writePNGHeader(t, filepath.Join(dir, "cover.png"), maxCoverDimension+1, 1)
		if _, err := discoverDirectoryCover(dir); !errors.Is(err, ErrNotFound) {
			t.Fatalf("wide error = %v", err)
		}
	})
	t.Run("total pixels", func(t *testing.T) {
		dir := t.TempDir()
		writePNGHeader(t, filepath.Join(dir, "cover.png"), 8000, 5001)
		if _, err := discoverDirectoryCover(dir); !errors.Is(err, ErrNotFound) {
			t.Fatalf("pixel error = %v", err)
		}
	})
	t.Run("corrupt", func(t *testing.T) {
		dir := t.TempDir()
		writeTestFile(t, filepath.Join(dir, "cover.jpg"), []byte("broken"))
		if _, err := discoverDirectoryCover(dir); !errors.Is(err, ErrNotFound) {
			t.Fatalf("corrupt error = %v", err)
		}
	})
}

func TestJPEGResultGateUsesExactFifteenPercentBoundary(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cover.jpg")
	writeJPEG(t, path, 16, 16)
	const originalSize = int64(1_100_000)
	padFile(t, path, originalSize)
	descriptor, err := discoverDirectoryCover(dir)
	if err != nil || !descriptor.RequiresRender {
		t.Fatalf("descriptor = %+v/%v", descriptor, err)
	}

	loader := NewLoader(nil)
	calls := 0
	loader.transcode = func(context.Context, Descriptor) ([]byte, error) {
		calls++
		return make([]byte, originalSize*85/100), nil
	}
	kept, err := loader.Render(context.Background(), descriptor)
	if err != nil || int64(len(kept)) != originalSize*85/100 {
		t.Fatalf("exact boundary = %d/%v", len(kept), err)
	}

	loader.transcode = func(context.Context, Descriptor) ([]byte, error) {
		calls++
		return make([]byte, originalSize*85/100+1), nil
	}
	original, err := loader.Render(context.Background(), descriptor)
	if err != nil || int64(len(original)) != originalSize {
		t.Fatalf("below required savings = %d/%v", len(original), err)
	}
	wantOriginal, _ := os.ReadFile(path)
	if !bytes.Equal(original, wantOriginal) {
		t.Fatal("JPEG below the savings gate did not return the original bytes")
	}
	_, renders, _ := loader.CacheStats()
	if calls != 2 || renders != 0 {
		t.Fatalf("transcode/cache calls = %d/%d", calls, renders)
	}
}

func TestTriggeredJPEGFailureNeverFallsBackToLargeOriginal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cover.jpg")
	writeJPEG(t, path, 16, 16)
	padFile(t, path, compressCoverBytes+1)
	descriptor, err := discoverDirectoryCover(dir)
	if err != nil || !descriptor.RequiresRender {
		t.Fatalf("descriptor = %+v/%v", descriptor, err)
	}
	want := errors.New("ffmpeg failed")
	loader := NewLoader(nil)
	loader.transcode = func(context.Context, Descriptor) ([]byte, error) { return nil, want }
	data, err := loader.Render(context.Background(), descriptor)
	if !errors.Is(err, want) || data != nil {
		t.Fatalf("failed conversion returned %d original bytes with error %v", len(data), err)
	}
}

func TestTriggeredPNGBecomesWhiteBackground1024JPEG(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg is required")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "cover.png")
	img := image.NewNRGBA(image.Rect(0, 0, 3000, 3000))
	for y := 1000; y < 2000; y++ {
		for x := 1000; x < 2000; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: 220, G: 30, B: 20, A: 180})
		}
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(file, img); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	descriptor, err := discoverDirectoryCover(dir)
	if err != nil || !descriptor.RequiresRender || descriptor.ContentType != "image/jpeg" {
		t.Fatalf("descriptor = %+v/%v", descriptor, err)
	}
	loader := NewLoader(nil)
	data, err := loader.Render(context.Background(), descriptor)
	if err != nil {
		t.Fatal(err)
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || format != "jpeg" || config.Width != 1024 || config.Height != 1024 {
		t.Fatalf("converted config = %+v format=%q err=%v", config, format, err)
	}
	decoded, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	r, g, b, _ := decoded.At(10, 10).RGBA()
	if r < 0xe000 || g < 0xe000 || b < 0xe000 {
		t.Fatalf("transparent corner was not composited on white: %#x %#x %#x", r, g, b)
	}
}

func TestTriggeredOddDimensionsDoNotUpscaleOrFail(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg is required")
	}
	dir := t.TempDir()
	writePNG(t, filepath.Join(dir, "cover.png"), 1537, 1, false)
	descriptor, err := discoverDirectoryCover(dir)
	if err != nil || !descriptor.RequiresRender {
		t.Fatalf("descriptor = %+v/%v", descriptor, err)
	}
	data, err := NewLoader(nil).Render(context.Background(), descriptor)
	if err != nil {
		t.Fatal(err)
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || format != "jpeg" || config.Width != 1024 || config.Height != 1 {
		t.Fatalf("odd output = %+v format=%q err=%v", config, format, err)
	}
}

func TestEmbeddedCoverFallsBackTo1024JPEG(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg is required")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe is required")
	}
	dir := t.TempDir()
	artPath := filepath.Join(dir, "art.png")
	writePNG(t, artPath, 1800, 900, false)
	audioPath := filepath.Join(dir, "track.mp3")
	command := exec.Command(
		"ffmpeg", "-v", "error", "-nostdin",
		"-f", "lavfi", "-i", "anullsrc=r=8000:cl=mono", "-i", artPath,
		"-map", "0:a:0", "-map", "1:v:0", "-t", "0.1",
		"-c:a", "libmp3lame", "-c:v", "mjpeg", "-disposition:v:0", "attached_pic", audioPath,
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("create embedded-cover fixture: %v: %s", err, output)
	}

	metadataProbe := stream.NewMetadataProbe(nil)
	loader := NewLoader(nil, Config{EmbeddedProbe: metadataProbe.ProbeEmbedded})
	descriptor, err := loader.Describe(context.Background(), audioPath)
	if err != nil {
		t.Fatal(err)
	}
	if descriptor.Kind != Embedded || descriptor.ContentType != "image/jpeg" || descriptor.Name != "cover.jpg" || !descriptor.RequiresRender {
		t.Fatalf("embedded descriptor = %+v", descriptor)
	}
	data, err := loader.Render(context.Background(), descriptor)
	if err != nil {
		t.Fatal(err)
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || format != "jpeg" || config.Width != 1024 || config.Height != 512 {
		t.Fatalf("embedded output = %+v format=%q err=%v", config, format, err)
	}
}

func TestSmallPNGStaysOriginalAndRetainsLength(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cover.png")
	writePNG(t, path, 32, 32, true)
	descriptor, err := discoverDirectoryCover(dir)
	if err != nil {
		t.Fatal(err)
	}
	if descriptor.RequiresRender || descriptor.ContentType != "image/png" || descriptor.ContentLength() == "" || descriptor.Name != "cover.png" {
		t.Fatalf("small PNG descriptor = %+v length=%q", descriptor, descriptor.ContentLength())
	}
}

func TestRenderSingleflightEndsWithoutResultCache(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cover.jpg")
	writeJPEG(t, path, 4, 4)
	info, _ := os.Stat(path)
	descriptor := Descriptor{
		Kind: Fallback, ContentType: "image/jpeg", OriginalContentType: "image/png", Name: "cover.jpg",
		ETag: `"one"`, FilePath: path, RequiresRender: true,
		fileSize: info.Size(), fileModNano: info.ModTime().UnixNano(),
	}
	loader := NewLoader(nil)
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	calls := 0
	loader.transcode = func(ctx context.Context, _ Descriptor) ([]byte, error) {
		calls++
		once.Do(func() { close(started) })
		select {
		case <-release:
			return []byte("jpeg"), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	const waiters = 32
	results := make(chan error, waiters)
	for range waiters {
		go func() {
			_, err := loader.Render(context.Background(), descriptor)
			results <- err
		}()
	}
	<-started
	waitFor(t, time.Second, func() bool {
		loader.mu.Lock()
		defer loader.mu.Unlock()
		flight := loader.renderCalls[descriptor.ETag]
		return flight != nil && flight.waiters == waiters
	})
	close(release)
	for range waiters {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Fatalf("singleflight calls = %d", calls)
	}
	if _, err := loader.Render(context.Background(), descriptor); err != nil {
		t.Fatal(err)
	}
	_, renders, _ := loader.CacheStats()
	if calls != 2 || renders != 0 {
		t.Fatalf("sequential render/cache = %d/%d", calls, renders)
	}
}

func TestCanceledRenderTerminatesProcessAndReleasesAuxSlot(t *testing.T) {
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skip("sleep is required")
	}
	dir := t.TempDir()
	ffmpeg := filepath.Join(dir, "ffmpeg")
	if err := os.WriteFile(ffmpeg, []byte("#!/bin/sh\nexec sleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	path := filepath.Join(dir, "cover.png")
	writePNG(t, path, 4, 4, false)
	info, _ := os.Stat(path)
	descriptor := Descriptor{
		Kind: Fallback, ContentType: "image/jpeg", OriginalContentType: "image/png", Name: "cover.jpg",
		ETag: `"cancel"`, FilePath: path, RequiresRender: true,
		fileSize: info.Size(), fileModNano: info.ModTime().UnixNano(),
	}
	manager := mediaexec.NewManager(mediaexec.ManagerConfig{
		MaxSessions: 2, AuxReserved: 1, TranscodeWaiters: 1, AuxWaiters: 1, WaitTimeout: time.Second,
	})
	loader := NewLoader(manager, Config{TaskTimeout: 15 * time.Second})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := loader.Render(ctx, descriptor)
		done <- err
	}()
	waitFor(t, time.Second, func() bool { return manager.Stats().ActiveAux == 1 })
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("render cancellation = %v", err)
	}
	waitFor(t, time.Second, func() bool { return manager.Stats().ActiveAux == 0 })
}

func TestOpenFallbackRejectsChangedIdentity(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cover.jpg")
	writeJPEG(t, path, 4, 4)
	descriptor, err := discoverDirectoryCover(dir)
	if err != nil {
		t.Fatal(err)
	}
	loader := NewLoader(nil)
	file, err := loader.OpenFallback(descriptor)
	if err != nil {
		t.Fatal(err)
	}
	file.Close()
	if err := os.WriteFile(path, []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loader.OpenFallback(descriptor); !errors.Is(err, ErrStale) {
		t.Fatalf("changed fallback error = %v", err)
	}
}

func TestDirectoryCoverRejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(t.TempDir(), "target.jpg")
	writeJPEG(t, target, 4, 4)
	if err := os.Symlink(target, filepath.Join(dir, "cover.jpg")); err != nil {
		t.Fatal(err)
	}
	loader := NewLoader(nil)
	if _, err := loader.DescribeDirectory(context.Background(), dir); !errors.Is(err, ErrNotFound) {
		t.Fatalf("symlink cover error = %v", err)
	}
}

func writeTestFile(t *testing.T, path string, data []byte) string {
	t.Helper()
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeJPEG(t *testing.T, path string, width, height int) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	if err := jpeg.Encode(file, img, &jpeg.Options{Quality: 90}); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func writePNG(t *testing.T, path string, width, height int, transparent bool) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	if !transparent {
		for y := 0; y < height; y++ {
			for x := 0; x < width; x++ {
				img.SetNRGBA(x, y, color.NRGBA{R: 40, G: 80, B: 120, A: 255})
			}
		}
	}
	if err := png.Encode(file, img); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func writePNGHeader(t *testing.T, path string, width, height int) {
	t.Helper()
	var data bytes.Buffer
	data.Write([]byte("\x89PNG\r\n\x1a\n"))
	var ihdr [13]byte
	binary.BigEndian.PutUint32(ihdr[0:4], uint32(width))
	binary.BigEndian.PutUint32(ihdr[4:8], uint32(height))
	ihdr[8], ihdr[9], ihdr[10], ihdr[11], ihdr[12] = 8, 6, 0, 0, 0
	writePNGChunk(&data, "IHDR", ihdr[:])
	writeTestFile(t, path, data.Bytes())
}

func writePNGChunk(buffer *bytes.Buffer, name string, payload []byte) {
	var size [4]byte
	binary.BigEndian.PutUint32(size[:], uint32(len(payload)))
	buffer.Write(size[:])
	buffer.WriteString(name)
	buffer.Write(payload)
	checksum := crc32.NewIEEE()
	_, _ = checksum.Write([]byte(name))
	_, _ = checksum.Write(payload)
	var sum [4]byte
	binary.BigEndian.PutUint32(sum[:], checksum.Sum32())
	buffer.Write(sum[:])
}

func padFile(t *testing.T, path string, size int64) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() > size {
		t.Fatalf("cannot pad %s from %d down to %d", path, info.Size(), size)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(size); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func waitFor(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition did not become true")
}
