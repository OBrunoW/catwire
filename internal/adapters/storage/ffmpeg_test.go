package storage

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestFFmpegConverterProducesOggOpus(t *testing.T) {
	binary, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	directory := t.TempDir()
	input := filepath.Join(directory, "input.m4a")
	generate := exec.Command(binary, "-nostdin", "-loglevel", "error", "-f", "lavfi", "-i", "sine=duration=1", "-c:a", "aac", input)
	if output, err := generate.CombinedOutput(); err != nil {
		t.Skipf("cannot generate sample audio: %v: %s", err, output)
	}
	output := filepath.Join(directory, "output.ogg")

	if err := NewFFmpegConverter(binary).ToOggOpus(context.Background(), input, output); err != nil {
		t.Fatal(err)
	}

	content, err := os.ReadFile(output)
	if err != nil || len(content) < 4 || string(content[:4]) != "OggS" {
		t.Fatalf("unexpected output: err = %v", err)
	}
}

func TestFFmpegConverterReportsFailure(t *testing.T) {
	directory := t.TempDir()

	err := NewFFmpegConverter("definitely-not-ffmpeg").ToOggOpus(
		context.Background(), filepath.Join(directory, "in"), filepath.Join(directory, "out"),
	)

	if err == nil {
		t.Fatal("expected error")
	}
}
