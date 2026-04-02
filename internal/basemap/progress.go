package basemap

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type downloadProgress struct {
	label   string
	total   int64
	initial int64

	started    time.Time
	lastLogAt  time.Time
	lastLogged int64

	minInterval time.Duration
	minDelta    int64
	writer      io.Writer

	bytes atomic.Int64

	done chan struct{}
	once sync.Once
	wg   sync.WaitGroup
	mu   sync.Mutex
}

func newDownloadProgress(label string, total, initial int64, writer io.Writer) *downloadProgress {
	if initial < 0 {
		initial = 0
	}
	if total < 0 {
		total = 0
	}
	if writer == nil {
		writer = os.Stderr
	}

	minDelta := total / 200
	const minAbsoluteDelta = 256 << 20
	if minDelta < minAbsoluteDelta {
		minDelta = minAbsoluteDelta
	}

	now := time.Now()
	p := &downloadProgress{
		label:       label,
		total:       total,
		initial:     initial,
		started:     now,
		lastLogAt:   now,
		lastLogged:  initial,
		minInterval: 30 * time.Second,
		minDelta:    minDelta,
		writer:      writer,
		done:        make(chan struct{}),
	}
	p.bytes.Store(initial)
	return p
}

func (p *downloadProgress) start(ctx context.Context, status string) {
	p.log(status, true)

	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		ticker := time.NewTicker(p.minInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				p.log("", false)
			case <-p.done:
				return
			case <-ctx.Done():
				return
			}
		}
	}()
}

func (p *downloadProgress) add(n int64) {
	if n <= 0 {
		return
	}
	p.bytes.Add(n)
	p.log("", false)
}

type progressReader struct {
	reader   io.Reader
	progress *downloadProgress
}

func (r *progressReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if n > 0 {
		r.progress.add(int64(n))
	}
	return n, err
}

func (p *downloadProgress) finish(status string) {
	p.stop()
	p.log(status, true)
}

func (p *downloadProgress) stop() {
	p.once.Do(func() {
		close(p.done)
	})
	p.wg.Wait()
}

func (p *downloadProgress) log(status string, force bool) {
	current := p.bytes.Load()

	p.mu.Lock()
	defer p.mu.Unlock()

	now := time.Now()
	if !force {
		if p.total > 0 && current >= p.total {
			return
		}
		if current-p.lastLogged < p.minDelta && now.Sub(p.lastLogAt) < p.minInterval {
			return
		}
	}

	p.lastLogAt = now
	p.lastLogged = current
	fmt.Fprintln(p.writer, p.formatLine(current, status, now))
}

func (p *downloadProgress) formatLine(current int64, status string, now time.Time) string {
	if current < 0 {
		current = 0
	}
	if p.total > 0 && current > p.total {
		current = p.total
	}

	elapsed := now.Sub(p.started)
	downloaded := current - p.initial
	if downloaded < 0 {
		downloaded = 0
	}

	prefix := "download " + p.label
	if status != "" {
		prefix += " " + status
	}

	if p.total <= 0 {
		line := fmt.Sprintf("%s: %s downloaded", prefix, formatBytes(float64(current)))
		if downloaded > 0 && elapsed > 0 {
			rate := float64(downloaded) / elapsed.Seconds()
			line += fmt.Sprintf(" at %s", formatBytesPerSecond(rate))
		}
		return line
	}

	percent := 0.0
	if p.total > 0 {
		percent = float64(current) * 100 / float64(p.total)
	}

	line := fmt.Sprintf("%s: %s %6.2f%% %s / %s", prefix, renderBar(current, p.total, 24), percent, formatBytes(float64(current)), formatBytes(float64(p.total)))

	if downloaded > 0 && elapsed > 0 {
		rate := float64(downloaded) / elapsed.Seconds()
		if rate > 0 {
			if status == "completed" {
				line += fmt.Sprintf(" in %s avg %s", elapsed.Round(time.Second), formatBytesPerSecond(rate))
			} else if current < p.total {
				line += fmt.Sprintf(" at %s", formatBytesPerSecond(rate))
				remaining := float64(p.total-current) / rate
				if remaining > 0 {
					line += fmt.Sprintf(" eta %s", time.Duration(remaining*float64(time.Second)).Round(time.Second))
				}
			} else {
				line += fmt.Sprintf(" at %s", formatBytesPerSecond(rate))
			}
		}
	}

	return line
}

func renderBar(current, total int64, width int) string {
	if total <= 0 || width <= 0 {
		return ""
	}

	if current < 0 {
		current = 0
	}
	if current > total {
		current = total
	}

	filled := int(float64(current) * float64(width) / float64(total))
	if filled >= width {
		return "[" + strings.Repeat("=", width) + "]"
	}
	if filled <= 0 {
		return "[" + strings.Repeat("-", width) + "]"
	}
	return "[" + strings.Repeat("=", filled-1) + ">" + strings.Repeat("-", width-filled) + "]"
}

func formatBytes(v float64) string {
	if v < 0 {
		v = 0
	}
	units := []string{"B", "KiB", "MiB", "GiB", "TiB", "PiB"}
	unit := 0
	for v >= 1024 && unit < len(units)-1 {
		v /= 1024
		unit++
	}
	if unit == 0 {
		return fmt.Sprintf("%.0f %s", v, units[unit])
	}
	return fmt.Sprintf("%.2f %s", v, units[unit])
}

func formatBytesPerSecond(v float64) string {
	return formatBytes(v) + "/s"
}
