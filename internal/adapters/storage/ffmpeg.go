package storage

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/OBrunoW/catwire/internal/application/media"
)

const defaultFFmpegPath = "ffmpeg"

type FFmpegConverter struct {
	binary string
}

func NewFFmpegConverter(binary string) *FFmpegConverter {
	if binary == "" {
		binary = defaultFFmpegPath
	}
	return &FFmpegConverter{binary: binary}
}

func (c *FFmpegConverter) ToOggOpus(ctx context.Context, inputPath, outputPath string) error {
	command := exec.CommandContext(ctx, c.binary,
		"-nostdin", "-hide_banner", "-loglevel", "error", "-y",
		"-i", inputPath,
		"-vn", "-map_metadata", "-1",
		"-c:a", "libopus", "-b:a", "32k", "-ar", "48000", "-ac", "1",
		"-f", "ogg", outputPath,
	)
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("storage: ffmpeg conversion failed: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

var _ media.Converter = (*FFmpegConverter)(nil)
