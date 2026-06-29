package transfer

import (
	"io"
	"sync"
	"sync/atomic"
	"time"
)

// ProgressTracker tracks bytes transferred and periodically calls
// a progress callback with speed, ETA, and percentage.
type ProgressTracker struct {
	totalBytes int64
	transferred int64
	callback   func(TransferProgress)
	interval   time.Duration
	startTime  time.Time
	stopCh     chan struct{}
	mu         sync.Mutex
	running    bool
}

// NewProgressTracker creates a ProgressTracker.
func NewProgressTracker(totalBytes int64, callback func(TransferProgress), interval time.Duration) *ProgressTracker {
	return &ProgressTracker{
		totalBytes: totalBytes,
		callback:   callback,
		interval:   interval,
		stopCh:     make(chan struct{}),
	}
}

// SetTransferred sets the initial transferred byte count (for resume).
func (t *ProgressTracker) SetTransferred(n int64) {
	atomic.StoreInt64(&t.transferred, n)
}

// BytesTransferred returns the total bytes transferred.
func (t *ProgressTracker) BytesTransferred() int64 {
	return atomic.LoadInt64(&t.transferred)
}

// Start begins the periodic progress callback goroutine.
func (t *ProgressTracker) Start() {
	t.mu.Lock()
	if t.running {
		t.mu.Unlock()
		return
	}
	t.running = true
	t.startTime = time.Now()
	t.mu.Unlock()

	if t.callback == nil {
		return
	}

	go t.loop()
}

// Stop stops the periodic progress callback.
func (t *ProgressTracker) Stop() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.running {
		return
	}
	t.running = false
	select {
	case <-t.stopCh:
		// already closed
	default:
		close(t.stopCh)
	}
	// Emit final progress
	if t.callback != nil {
		t.emit()
	}
}

func (t *ProgressTracker) loop() {
	ticker := time.NewTicker(t.interval)
	defer ticker.Stop()

	for {
		select {
		case <-t.stopCh:
			return
		case <-ticker.C:
			t.emit()
		}
	}
}

func (t *ProgressTracker) emit() {
	if t.callback == nil {
		return
	}

	transferred := atomic.LoadInt64(&t.transferred)
	elapsed := time.Since(t.startTime).Seconds()
	if elapsed <= 0 {
		elapsed = 0.001
	}

	speedBPS := int64(float64(transferred) / elapsed)
	var eta time.Duration
	var pct float64

	if t.totalBytes > 0 {
		remaining := t.totalBytes - transferred
		if speedBPS > 0 {
			eta = time.Duration(float64(remaining)/float64(speedBPS)) * time.Second
		}
		pct = float64(transferred) / float64(t.totalBytes) * 100
		if pct > 100 {
			pct = 100
		}
	}

	t.callback(TransferProgress{
		BytesTransferred: transferred,
		TotalBytes:       t.totalBytes,
		SpeedBPS:         speedBPS,
		ETA:              eta,
		Percentage:       pct,
	})
}

// WrapReader wraps an io.Reader and tracks bytes read.
func (t *ProgressTracker) WrapReader(r io.Reader) io.Reader {
	return &progressReader{
		reader:  r,
		tracker: t,
	}
}

// WrapWriter wraps an io.Writer and tracks bytes written.
func (t *ProgressTracker) WrapWriter(w io.Writer) io.Writer {
	return &progressWriter{
		writer:  w,
		tracker: t,
	}
}

// progressReader wraps an io.Reader and increments the tracker on each read.
type progressReader struct {
	reader  io.Reader
	tracker *ProgressTracker
}

func (r *progressReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if n > 0 {
		atomic.AddInt64(&r.tracker.transferred, int64(n))
	}
	return n, err
}

// progressWriter wraps an io.Writer and increments the tracker on each write.
type progressWriter struct {
	writer  io.Writer
	tracker *ProgressTracker
}

func (w *progressWriter) Write(p []byte) (int, error) {
	n, err := w.writer.Write(p)
	if n > 0 {
		atomic.AddInt64(&w.tracker.transferred, int64(n))
	}
	return n, err
}

// --- Transfer checkpoint storage ---

// TransferCheckpoint stores the state of a transfer for resume.
type TransferCheckpoint struct {
	ID              int64  `json:"id"`
	TransferID      string `json:"transferId"`  // unique ID for this transfer
	FileName        string `json:"fileName"`     // name of the file being transferred
	ByteOffset      int64  `json:"byteOffset"`   // bytes transferred so far
	TotalBytes      int64  `json:"totalBytes"`    // total file size
	Checksum        string `json:"checksum"`      // checksum of transferred data (if available)
	Strategy        string `json:"strategy"`      // transfer strategy used
	SourcePath      string `json:"sourcePath"`
	DestPath        string `json:"destPath"`
	CreatedAt       string `json:"createdAt"`
	UpdatedAt       string `json:"updatedAt"`
}

// CheckpointStore stores transfer checkpoints in SQLite.
type CheckpointStore interface {
	// SaveCheckpoint saves or updates a transfer checkpoint.
	SaveCheckpoint(cp TransferCheckpoint) error
	// GetCheckpoint retrieves a checkpoint by transfer ID and file name.
	GetCheckpoint(transferID, fileName string) (*TransferCheckpoint, error)
	// DeleteCheckpoint removes a checkpoint.
	DeleteCheckpoint(transferID, fileName string) error
	// DeleteCheckpoints removes all checkpoints for a transfer.
	DeleteCheckpoints(transferID string) error
}

// NoopCheckpointStore is a CheckpointStore that does nothing.
// Used when checkpoint persistence is not needed.
type NoopCheckpointStore struct{}

func (NoopCheckpointStore) SaveCheckpoint(cp TransferCheckpoint) error                  { return nil }
func (NoopCheckpointStore) GetCheckpoint(transferID, fileName string) (*TransferCheckpoint, error) {
	return nil, nil
}
func (NoopCheckpointStore) DeleteCheckpoint(transferID, fileName string) error          { return nil }
func (NoopCheckpointStore) DeleteCheckpoints(transferID string) error                   { return nil }
