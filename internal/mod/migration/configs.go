package migration

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"meshium/internal/shared"
)

// configExclusions is a list of file paths and directory prefixes that must
// never be overwritten during config migration. These are OS-critical files
// that would break the target server if replaced.
var configExclusions = []string{
	"/etc/fstab",
	"/etc/hostname",
	"/etc/machine-id",
	"/etc/hosts",
	"/etc/shadow",
	"/etc/passwd",
	"/etc/group",
	"/etc/subuid",
	"/etc/subgid",
	"/etc/resolv.conf",
	"/etc/network/",
	"/etc/netplan/",
	"/etc/sysconfig/network-scripts/",
	"/etc/udev/",
	"/etc/crypttab",
	"/etc/mdadm.conf",
	"/etc/dracut.conf",
	"/etc/kernel/",
	"/etc/grub.d/",
	"/etc/default/grub",
}

// maxConfigFileSize is the per-file cap for collected config content. A plan
// stores every file's full body in one migration_steps row, so an unbounded
// /etc scan writes ~100MB+ per plan (logs, caches, state DBs in /etc) and
// bloats SQLite. 1MiB covers real config files; anything larger is skipped.
// ponytail: raise or make per-path configurable if large config files must
// be migrated verbatim.
const maxConfigFileSize = 1 << 20 // 1 MiB

// isExcluded returns true if the given path matches any exclusion entry.
// Matches both exact file paths and directory prefixes (ending with /).
func isExcluded(path string) bool {
	for _, excl := range configExclusions {
		if strings.HasSuffix(excl, "/") {
			if strings.HasPrefix(path, excl) {
				return true
			}
		} else {
			if path == excl {
				return true
			}
		}
	}
	return false
}

// ConfigsData holds collected config files from the source server.
type ConfigsData struct {
	Files map[string][]byte `json:"files"` // path -> content
	// Meta carries each file's ownership and permission bits. Applying content
	// alone silently reset a 0600 root-owned secret to the SFTP default umask
	// on the target, widening it. Absent for payloads planned before this was
	// captured — Apply then leaves target metadata untouched.
	Meta  map[string]FileMeta `json:"meta,omitempty"`
	Count int                 `json:"count"`
}

// FileMeta is the ownership/permission metadata of one config file. Names are
// preferred over numeric ids: uid 1001 can be a different account on the
// target, whereas "www-data" is the identity that actually matters.
type FileMeta struct {
	Mode  int64  `json:"mode"`            // permission bits, e.g. 0600
	Uname string `json:"uname,omitempty"` // owner name
	Gname string `json:"gname,omitempty"` // group name
}

// ConfigsBackup holds the target's original config files.
type ConfigsBackup struct {
	Files map[string][]byte   `json:"files"`
	Meta  map[string]FileMeta `json:"meta,omitempty"`
}

// ConfigsCollector collects config files from the source server via SFTP.
type ConfigsCollector struct {
	Paths []string // paths to collect (default: /etc/)
}

// Collect downloads config files from the source server using tar streaming
// for maximum performance (single SSH round-trip instead of hundreds of SFTP reads).
func (c *ConfigsCollector) Collect(ctx context.Context, ssh SSHExecuter) (CategoryData, error) {
	paths := c.Paths
	if len(paths) == 0 {
		paths = []string{"/etc/"}
	}

	data := ConfigsData{
		Files: make(map[string][]byte),
	}

	for _, path := range paths {
		cleanPath := strings.TrimRight(path, "/")
		if cleanPath == "" {
			cleanPath = "/etc"
		}

		// Build a bounded find: stay shallow (-maxdepth 4, real config lives
		// near the top of /etc), prune the known huge subtrees (cert stores,
		// font/terminfo databases) that contain tens of thousands of files and
		// would otherwise produce a ~100MB+ archive, and cap per-file size to
		// maxConfigFileSize so a single large file can't blow the capture.
		//
		// The tar is gzipped before base64. A real /etc at this depth is ~3 MB
		// of base64 uncompressed, which blows the 1 MiB ExecContext cap; the
		// command then errors and every file falls to the per-file SFTP crawl
		// below, which takes minutes. Config files are text and compress about
		// 6x, bringing the same capture to ~550 KB. parseTarArchive re-checks
		// the per-file size as a final guard.
		files, meta, err := c.collectArchive(ctx, ssh, cleanPath)
		if err != nil {
			// Fallback: try individual file download (old method). collectSlow
			// is itself bounded, so even this path can never hang.
			c.collectSlow(ctx, ssh, cleanPath, &data)
			continue
		}

		for path, content := range files {
			// tar strips the leading "/", so archive keys arrive relative
			// ("etc/host.conf") while collectSlow stores absolute
			// ("/etc/host.conf"). Normalise here so one format reaches every
			// consumer: isExcluded and the dry-run both match against absolute
			// paths, and against a relative key the exclusion list — the guard
			// that stops /etc/fstab and /etc/passwd being overwritten — silently
			// matched nothing.
			abs := absConfigPath(path)
			if isExcluded(abs) {
				continue
			}
			data.Files[abs] = content
			if m, ok := meta[path]; ok {
				if data.Meta == nil {
					data.Meta = make(map[string]FileMeta)
				}
				data.Meta[abs] = m
			}
		}
	}

	data.Count = len(data.Files)

	raw, _ := json.Marshal(data)
	return CategoryData{Type: "configs", Data: raw}, nil
}

// maxConfigFiles caps the number of files the slow fallback will download.
// A pathological /etc (hundreds of thousands of files) must never turn this
// fallback into an unbounded SFTP loop — once we hit the cap we stop, leaving
// the plan able to proceed with what it has rather than hanging.
const maxConfigFiles = 2000

// collectSlow is the fallback method that downloads files one-by-one via SFTP.
// It is itself bounded: a shallow find, a per-file size guard, a hard file
// count cap, and a per-download context timeout — so it can never hang even
// if the primary tar pipeline failed on a very large /etc.
func (c *ConfigsCollector) collectSlow(ctx context.Context, ssh SSHExecuter, path string, data *ConfigsData) {
	stdout, _, _, err := ssh.ExecContext(ctx, fmt.Sprintf("find %s -maxdepth 4 -type f 2>/dev/null", shared.ShellQuote(path)))
	if err != nil {
		return
	}

	for _, file := range strings.Split(strings.TrimSpace(stdout), "\n") {
		if len(data.Files) >= maxConfigFiles {
			break
		}
		file = strings.TrimSpace(file)
		if file == "" || isExcluded(file) {
			continue
		}
		// Per-download timeout: a single stuck SFTP transfer must not block the
		// whole plan. If the context fires we bail out of the loop.
		dlCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		buf := new(bytes.Buffer)
		// Download has no context param on the interface, so honor the timeout
		// by racing it against the parent context via a short goroutine.
		done := make(chan error, 1)
		go func() { done <- ssh.Download(file, buf) }()
		select {
		case <-dlCtx.Done():
			cancel()
			return
		case derr := <-done:
			cancel()
			if derr != nil {
				continue
			}
		}
		if buf.Len() > maxConfigFileSize {
			continue
		}
		data.Files[file] = buf.Bytes()
	}
}

// buildExcludeArgs builds find exclude arguments for OS-critical files.
func buildExcludeArgs() string {
	var args []string
	for _, excl := range configExclusions {
		if strings.HasSuffix(excl, "/") {
			args = append(args, fmt.Sprintf("-not -path '%s*'", excl))
		} else {
			args = append(args, fmt.Sprintf("-not -name '%s'", filepath.Base(excl)))
		}
	}
	return strings.Join(args, " ")
}

// hugeConfigDirs are subtrees of /etc that contain tens of thousands of files
// (certificate stores, font/terminfo databases) which are not real config to
// migrate and would otherwise dominate the archive. Prune them so the find
// stays shallow and fast.
var hugeConfigDirs = []string{
	"/etc/ssl",
	"/etc/ssl/certs",
	"/etc/fonts",
	"/etc/terminfo",
	"/etc/share",
}

// absConfigPath re-anchors a collected config key to its absolute system path.
// tar emits members without the leading "/", the SFTP fallback keeps it, and
// every consumer (exclusion checks, dry-run hashing, upload) means the absolute
// path — so keys are normalised to absolute at collection time.
func absConfigPath(p string) string {
	if strings.HasPrefix(p, "/") {
		return p
	}
	return "/" + p
}

// collectArchive captures one path as a single gzipped tar.
//
// It streams when the executer supports it. ExecContext caps captured stdout at
// 1 MiB, and a real /etc is well past that even gzipped (~5 MB on a host with a
// populated /etc/letsencrypt), so the buffered path errors with
// ErrOutputLimitExceeded and drops the whole category into collectSlow — which
// downloads files one at a time over SFTP, takes minutes, and stops at
// maxConfigFiles, silently truncating before it ever reaches /etc/ssh or
// /etc/fstab. ExecPipe has no such cap, which is exactly what it exists for.
//
// The buffered path is kept for executers that cannot stream (test mocks).
func (c *ConfigsCollector) collectArchive(ctx context.Context, ssh SSHExecuter, cleanPath string) (map[string][]byte, map[string]FileMeta, error) {
	findCmd := buildCollectFind(cleanPath)

	if streamer, ok := ssh.(StreamExecuter); ok {
		// No base64 on this path: the pipe is binary-safe, and skipping it
		// avoids inflating the transfer by a third.
		cmd := fmt.Sprintf(`%s 2>/dev/null | tar -czf - -T - 2>/dev/null`, findCmd)
		r, err := streamer.ExecPipe(ctx, cmd)
		if err != nil {
			return nil, nil, err
		}
		defer r.Close()

		zr, err := gzip.NewReader(io.LimitReader(r, maxConfigArchiveBytes))
		if err != nil {
			return nil, nil, fmt.Errorf("config archive is not valid gzip: %w", err)
		}
		defer zr.Close()

		return parseTarReader(zr)
	}

	stdout, _, _, err := ssh.ExecContext(ctx, fmt.Sprintf(
		`%s 2>/dev/null | tar -czf - -T - 2>/dev/null | base64`, findCmd))
	if err != nil {
		return nil, nil, err
	}
	gzData, err := base64Decode(stdout)
	if err != nil {
		return nil, nil, err
	}
	tarData, err := gunzip(gzData)
	if err != nil {
		return nil, nil, err
	}
	return parseTarArchive(tarData)
}

// gunzip decompresses the gzipped tar produced by the collect command. The
// decompressed size is bounded so a hostile or runaway archive cannot exhaust
// memory: the capture is already capped at 1 MiB of base64 on the wire, and a
// legitimate /etc expands to a few MB.
func gunzip(b []byte) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("config archive is not valid gzip: %w", err)
	}
	defer zr.Close()

	out, err := io.ReadAll(io.LimitReader(zr, maxConfigArchiveBytes))
	if err != nil {
		return nil, fmt.Errorf("decompressing config archive: %w", err)
	}
	return out, nil
}

// maxConfigArchiveBytes bounds the decompressed config tar.
const maxConfigArchiveBytes = 64 << 20 // 64 MiB

// buildCollectFind assembles the find expression Collect pipes into tar.
//
// Operator precedence matters: this reads as
//
//	(-path huge1 -prune) -o (-path huge2 -prune) -o (-type f <size> <excl> -print)
//
// so a pruned directory matches an earlier branch and is never printed, while
// only regular files that pass the size and exclusion tests reach -print.
func buildCollectFind(path string) string {
	return fmt.Sprintf(
		`find %s -maxdepth 4 %s -type f %s %s %s`,
		shared.ShellQuote(path), buildPruneArgs(), configSizeCapArg(), buildExcludeArgs(), configPrintArg(),
	)
}

// configSizeCapArg builds the per-file size cap for find.
//
// It must use the 'c' (bytes) suffix. GNU find rounds a file's size UP to the
// given unit before comparing, so `-size -1M` means "size rounded up to whole
// megabytes is < 1" — true only for zero-byte files. The previous
// `-size -1M` therefore skipped every non-empty config file.
func configSizeCapArg() string {
	return fmt.Sprintf("-size -%dc", maxConfigFileSize)
}

// configPrintArg terminates the find expression with an explicit -print.
//
// Without it, find applies an implicit -print to the ENTIRE expression, and
// since `-prune` evaluates to true the pruned directories are printed. Feeding
// a directory to `tar -T -` makes tar archive that whole subtree — pulling in
// exactly the cert/font/terminfo trees the prune exists to skip. With an
// explicit -print bound to the final branch, pruned directories match an
// earlier branch and are never printed.
func configPrintArg() string {
	return "-print"
}

// buildPruneArgs builds find -prune arguments for the huge subtrees. Pruning
// (rather than -not -path) stops find from even descending into them, which is
// what keeps a large /etc scan from enumerating hundreds of thousands of files.
func buildPruneArgs() string {
	var args []string
	for _, d := range hugeConfigDirs {
		args = append(args, fmt.Sprintf("-path '%s' -prune -o", d))
	}
	return strings.Join(args, " ")
}

// base64Decode decodes base64 encoded data, trimming whitespace.
func base64Decode(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, fmt.Errorf("empty base64 input")
	}
	return base64.StdEncoding.DecodeString(s)
}

// parseTarArchive parses a tar archive in memory and returns map[path]content.
// Files larger than maxConfigFileSize are skipped: a plan stores every config's
// full content in one migration_steps row, so without a cap a single /etc scan
// writes ~100MB+ per plan and bloats the DB (and stalls the step). Large files
// in /etc are almost always logs/caches/state DBs, not real config.
func parseTarArchive(data []byte) (map[string][]byte, map[string]FileMeta, error) {
	return parseTarReader(bytes.NewReader(data))
}

// parseTarReader reads a tar stream without first buffering the whole archive.
// It returns file contents and their ownership/permission metadata, which tar
// records in every header and the collector previously discarded.
func parseTarReader(src io.Reader) (map[string][]byte, map[string]FileMeta, error) {
	files := make(map[string][]byte)
	meta := make(map[string]FileMeta)
	r := tar.NewReader(src)
	for {
		header, err := r.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return files, meta, err
		}
		if header.Typeflag != tar.TypeReg {
			continue
		}
		if header.Size > maxConfigFileSize {
			// Discard the body so the tar reader stays aligned.
			io.CopyN(io.Discard, r, header.Size)
			continue
		}
		content, err := io.ReadAll(r)
		if err != nil {
			continue
		}
		files[header.Name] = content
		meta[header.Name] = FileMeta{
			Mode:  header.Mode & 0o7777,
			Uname: header.Uname,
			Gname: header.Gname,
		}
	}
	return files, meta, nil
}

// ConfigsApplier uploads config files to the target server.
type ConfigsApplier struct{}

// Backup saves the target's current config files.
func (a *ConfigsApplier) Backup(ctx context.Context, ssh SSHExecuter) (BackupData, error) {
	backup := ConfigsBackup{
		Files: make(map[string][]byte),
	}

	// Backup /etc/ on the target. Bounded the same way as collectSlow: a shallow
	// find, the OS-critical exclusion, a size guard and a per-download timeout so
	// a large target /etc can never turn the backup into an unbounded SFTP loop.
	stdout, _, _, err := ssh.ExecContext(ctx, "find /etc -maxdepth 4 -type f 2>/dev/null")
	if err != nil {
		return BackupData{}, err
	}

	for _, file := range strings.Split(strings.TrimSpace(stdout), "\n") {
		if len(backup.Files) >= maxConfigFiles {
			break
		}
		file = strings.TrimSpace(file)
		if file == "" {
			continue
		}
		// Skip OS-critical files — they should never be overwritten
		if isExcluded(file) {
			continue
		}
		dlCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		buf := new(bytes.Buffer)
		done := make(chan error, 1)
		go func() { done <- ssh.Download(file, buf) }()
		select {
		case <-dlCtx.Done():
			cancel()
			return BackupData{}, nil
		case derr := <-done:
			cancel()
			if derr != nil {
				continue
			}
		}
		if buf.Len() > maxConfigFileSize {
			continue
		}
		backup.Files[file] = buf.Bytes()
	}

	// Capture the target's own mode/owner so Rollback restores metadata too —
	// otherwise a rollback would rewrite the content but leave the widened
	// permissions Apply had set.
	backup.Meta = statFileMeta(ctx, ssh, keysOfBytes(backup.Files))

	raw, _ := json.Marshal(backup)
	return BackupData{Type: "configs", Data: raw}, nil
}

// keysOfBytes returns the sorted keys of a path→content map.
func keysOfBytes(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// statFileMeta reads mode/owner for the given paths in batches.
//
// `stat -c '%a %U %G %n'` prints e.g. "600 root root /etc/ssh/sshd_config".
// Paths are batched so a large /etc does not exceed ARG_MAX, and a failed
// batch is skipped rather than failing the backup — metadata is best-effort
// on top of content, which is the part rollback truly needs.
func statFileMeta(ctx context.Context, ssh SSHExecuter, paths []string) map[string]FileMeta {
	meta := make(map[string]FileMeta)
	const batch = 200
	for start := 0; start < len(paths); start += batch {
		end := start + batch
		if end > len(paths) {
			end = len(paths)
		}
		quoted := make([]string, 0, end-start)
		for _, p := range paths[start:end] {
			quoted = append(quoted, shared.ShellQuote(p))
		}
		out, _, exit, err := ssh.ExecContext(ctx,
			"stat -c '%a %U %G %n' "+strings.Join(quoted, " ")+" 2>/dev/null")
		if err != nil || exit != 0 {
			continue
		}
		for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
			// Split into at most 4 so a path containing spaces stays intact.
			fields := strings.SplitN(strings.TrimSpace(line), " ", 4)
			if len(fields) != 4 {
				continue
			}
			mode, perr := strconv.ParseInt(fields[0], 8, 64)
			if perr != nil {
				continue
			}
			meta[fields[3]] = FileMeta{Mode: mode, Uname: fields[1], Gname: fields[2]}
		}
	}
	if len(meta) == 0 {
		return nil
	}
	return meta
}

// applyFileMeta restores mode and ownership for one uploaded file.
//
// chmod runs before chown: chown clears setuid/setgid bits on most kernels, so
// doing it the other way round would silently drop them. Ownership uses NAMES
// (uname:gname) because a numeric uid means a different account on the target;
// a missing account makes chown fail, which is surfaced as a warning rather
// than failing the whole category — the file content is already correct.
func applyFileMeta(ctx context.Context, ssh SSHExecuter, dst string, meta map[string]FileMeta, onProgress StepCallback) {
	m, ok := meta[dst]
	if !ok {
		return
	}
	warn := func(format string, args ...interface{}) {
		if onProgress != nil {
			onProgress(WSMessage{Step: "configs:apply", Status: "warning", Value: fmt.Sprintf(format, args...)})
		}
	}
	if m.Mode != 0 {
		cmd := fmt.Sprintf("chmod %o %s", m.Mode, shared.ShellQuote(dst))
		if _, _, exit, err := ssh.ExecContext(ctx, cmd); err != nil || exit != 0 {
			warn("could not restore mode %o on %s (exit %d)", m.Mode, dst, exit)
		}
	}
	if m.Uname != "" {
		owner := m.Uname
		if m.Gname != "" {
			owner += ":" + m.Gname
		}
		cmd := fmt.Sprintf("chown %s %s", shared.ShellQuote(owner), shared.ShellQuote(dst))
		if _, _, exit, err := ssh.ExecContext(ctx, cmd); err != nil || exit != 0 {
			warn("could not restore owner %s on %s — does the account exist on the target? (exit %d)", owner, dst, exit)
		}
	}
}

// Apply uploads config files to the target server.
func (a *ConfigsApplier) Apply(ctx context.Context, ssh SSHExecuter, data CategoryData, onProgress StepCallback) error {
	var cd ConfigsData
	if err := json.Unmarshal(data.Data, &cd); err != nil {
		return err
	}

	if onProgress != nil {
		onProgress(WSMessage{
			Step:   "configs:apply",
			Status: "progress",
			Value:  fmt.Sprintf("Uploading %d config files", cd.Count),
		})
	}

	count := 0
	for path, content := range cd.Files {
		// Safety net: skip OS-critical files even if they somehow got into the data
		if isExcluded(path) {
			if onProgress != nil {
				onProgress(WSMessage{
					Step:   "configs:apply",
					Status: "warning",
					Value:  fmt.Sprintf("Skipping excluded file: %s", path),
				})
			}
			continue
		}
		// tar strips the leading "/" by default, so collected keys arrive as
		// relative (e.g. "etc/host.conf"). The intended destination is the
		// absolute system path, so re-anchor before upload — otherwise SFTP
		// writes under the login cwd and fails with "file does not exist".
		dst := path
		if !strings.HasPrefix(dst, "/") {
			dst = "/" + dst
		}
		// The target may not have the parent directory (e.g. the source ships
		// /etc/nginx/... but the target never installed nginx). SFTP Create
		// won't mkdir -p for us, so create the parent first; without this the
		// whole config phase fails on the first missing directory.
		if parent := filepath.Dir(dst); parent != "/" && parent != "." {
			if _, _, _, mErr := ssh.ExecContext(ctx, "mkdir -p "+shared.ShellQuote(parent)); mErr != nil {
				if onProgress != nil {
					onProgress(WSMessage{
						Step:   "configs:apply",
						Status: "warning",
						Value:  fmt.Sprintf("could not mkdir %s: %v", parent, mErr),
					})
				}
			}
		}
		if err := ssh.Upload(bytes.NewReader(content), dst); err != nil {
			if onProgress != nil {
				onProgress(WSMessage{
					Step:   "configs:apply",
					Status: "error",
					Error:  fmt.Sprintf("failed to upload %s: %v", dst, err),
				})
			}
			return fmt.Errorf("failed to upload %s: %w", dst, err)
		}
		// Restore the source's ownership and permissions. SFTP creates the file
		// with the login user's umask, so a 0600 root-owned secret would land
		// world-readable on the target without this. Payloads planned before
		// metadata capture carry no Meta and are left alone (target keeps its
		// own metadata) rather than being reset to a guessed default.
		applyFileMeta(ctx, ssh, dst, cd.Meta, onProgress)
		count++
		if onProgress != nil && count%10 == 0 {
			onProgress(WSMessage{
				Step:   "configs:apply",
				Status: "progress",
				Value:  fmt.Sprintf("Uploaded %d/%d", count, cd.Count),
			})
		}
	}

	if onProgress != nil {
		onProgress(WSMessage{
			Step:   "configs:apply",
			Status: "success",
			Value:  fmt.Sprintf("%d config files uploaded", count),
		})
	}

	return nil
}

// Rollback restores the target's original config files.
func (a *ConfigsApplier) Rollback(ctx context.Context, ssh SSHExecuter, backup BackupData) error {
	var cb ConfigsBackup
	if err := json.Unmarshal(backup.Data, &cb); err != nil {
		return err
	}

	for path, content := range cb.Files {
		if err := ssh.Upload(bytes.NewReader(content), path); err != nil {
			// Continue even if some files fail
			continue
		}
		// Restore the target's original mode/owner too. Without this a rollback
		// would put the old content back under whatever permissions Apply left
		// behind — undoing the change but not the exposure.
		applyFileMeta(ctx, ssh, path, cb.Meta, nil)
	}

	return nil
}
