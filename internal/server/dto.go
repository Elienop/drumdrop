// Package server hosts drumdrop's inbound HTTP API and SSE progress stream over
// the shared database.Store and progress Hub.
package server

import (
	"database/sql"
	"path/filepath"
	"strings"
	"time"

	"github.com/elienop/drumdrop/internal/database"
)

// The DTO types below are the JSON wire shapes for the API. They exist so the
// database structs' sql.Null* columns serialize as a bare null or scalar
// (e.g. "slug": null or "slug": "x") instead of leaking the {String,Valid}
// shape that sql.NullString marshals to by default. Nullable columns become
// pointers (nil → JSON null); non-nullable columns keep their plain Go types.

// updateFollowRequest is the PATCH /api/follows/{id} body. Only quality is
// editable; the follow's identity (kind/railcontent_id/slug/brand) is immutable.
type updateFollowRequest struct {
	Quality string `json:"quality"`
}

// FollowDTO is the JSON wire shape of a database.Follow.
type FollowDTO struct {
	ID            int64      `json:"id"`
	Kind          string     `json:"kind"`
	RailcontentID *int64     `json:"railcontent_id"`
	Slug          *string    `json:"slug"`
	Title         string     `json:"title"`
	Brand         string     `json:"brand"`
	Quality       string     `json:"quality"`
	AddedAt       time.Time  `json:"added_at"`
	LastSyncedAt  *time.Time `json:"last_synced_at"`
}

// LessonDTO is the JSON wire shape of a database.Lesson.
type LessonDTO struct {
	RailcontentID       int        `json:"railcontent_id"`
	Title               string     `json:"title"`
	ParentRailcontentID *int64     `json:"parent_railcontent_id"`
	Brand               string     `json:"brand"`
	Status              string     `json:"status"`
	Quality             *string    `json:"quality"`
	OutputDir           *string    `json:"output_dir"`
	VideoPath           *string    `json:"video_path"`
	Bytes               *int64     `json:"bytes"`
	Error               *string    `json:"error"`
	FollowID            *int64     `json:"follow_id"`
	FirstSeenAt         *time.Time `json:"first_seen_at"`
	DownloadedAt        *time.Time `json:"downloaded_at"`
	UpdatedAt           *time.Time `json:"updated_at"`
	// HasFiles is true exactly when a delete of this lesson would have files
	// to act on (database.Lesson.HasFiles: an output_dir, or a library record
	// that is not empty), whatever its status. Always present.
	HasFiles bool `json:"has_files"`
}

// JobDTO is the JSON wire shape of a database.Job.
type JobDTO struct {
	ID            int64      `json:"id"`
	FollowID      *int64     `json:"follow_id"`
	RailcontentID int        `json:"railcontent_id"`
	Status        string     `json:"status"`
	Attempts      int        `json:"attempts"`
	Error         *string    `json:"error"`
	CreatedAt     *time.Time `json:"created_at"`
	StartedAt     *time.Time `json:"started_at"`
	FinishedAt    *time.Time `json:"finished_at"`
}

// SummaryDTO is the JSON wire shape of the dashboard summary: lesson counts
// keyed by lesson status, job counts keyed by job status, and the total follow
// count. The Lessons and Jobs maps always carry every known enum value, with a
// zero count for statuses that have no rows, so the client can render a stable
// set of buckets without guessing the enum set.
type SummaryDTO struct {
	Follows int            `json:"follows"`
	Lessons map[string]int `json:"lessons"`
	Jobs    map[string]int `json:"jobs"`
	// Paused reflects the daemon's pause flag (false when no daemon is attached),
	// so the UI has a single source for the paused indicator.
	Paused bool `json:"paused"`
}

// nullInt64 returns a *int64 that is nil when n is NULL, or points at the value
// otherwise, so the field marshals to JSON null or a bare integer.
func nullInt64(n sql.NullInt64) *int64 {
	if !n.Valid {
		return nil
	}
	v := n.Int64
	return &v
}

// nullString returns a *string that is nil when s is NULL, or points at the
// value otherwise. A valid-but-empty string yields a pointer to "" (JSON ""),
// distinct from NULL (JSON null).
func nullString(s sql.NullString) *string {
	if !s.Valid {
		return nil
	}
	v := s.String
	return &v
}

// nullTime returns a *time.Time that is nil when t is NULL, or points at the
// value otherwise, so the field marshals to JSON null or an RFC3339 string.
func nullTime(t sql.NullTime) *time.Time {
	if !t.Valid {
		return nil
	}
	v := t.Time
	return &v
}

// followDTO maps a database.Follow to its wire shape.
func followDTO(f database.Follow) FollowDTO {
	return FollowDTO{
		ID:            f.ID,
		Kind:          f.Kind,
		RailcontentID: nullInt64(f.RailcontentID),
		Slug:          nullString(f.Slug),
		Title:         f.Title,
		Brand:         f.Brand,
		Quality:       f.Quality,
		AddedAt:       f.AddedAt,
		LastSyncedAt:  nullTime(f.LastSyncedAt),
	}
}

// lessonDTO maps a database.Lesson to its wire shape.
func lessonDTO(l database.Lesson) LessonDTO {
	return LessonDTO{
		RailcontentID:       l.RailcontentID,
		Title:               l.Title,
		ParentRailcontentID: nullInt64(l.ParentRailcontentID),
		Brand:               l.Brand,
		Status:              l.Status,
		Quality:             nullString(l.Quality),
		OutputDir:           nullString(l.OutputDir),
		VideoPath:           nullString(l.VideoPath),
		Bytes:               nullInt64(l.Bytes),
		Error:               nullString(l.Error),
		FollowID:            nullInt64(l.FollowID),
		FirstSeenAt:         nullTime(l.FirstSeenAt),
		DownloadedAt:        nullTime(l.DownloadedAt),
		UpdatedAt:           nullTime(l.UpdatedAt),
		HasFiles:            l.HasFiles(),
	}
}

// jobDTO maps a database.Job to its wire shape.
func jobDTO(j database.Job) JobDTO {
	return JobDTO{
		ID:            j.ID,
		FollowID:      nullInt64(j.FollowID),
		RailcontentID: j.RailcontentID,
		Status:        j.Status,
		Attempts:      j.Attempts,
		Error:         nullString(j.Error),
		CreatedAt:     nullTime(j.CreatedAt),
		StartedAt:     nullTime(j.StartedAt),
		FinishedAt:    nullTime(j.FinishedAt),
	}
}

// hostPath rewrites a stored container download path to its host equivalent when
// a host downloads dir is configured (DRUMDROP_HOST_DOWNLOADS_DIR), so the UI's
// "Copy path" yields a path that resolves on the host rather than the container's
// internal /downloads. A nil pointer, an unset HostDownloadsDir/DownloadsDir, or
// a path that is not under DownloadsDir is returned unchanged.
func (s *Server) hostPath(p *string) *string {
	if p == nil || s.cfg.HostDownloadsDir == "" || s.cfg.DownloadsDir == "" {
		return p
	}
	// A row recorded under a relative downloads folder (the ./downloads default,
	// before the roots were made absolute) is read as the OS reads it.
	stored := *p
	if abs, err := filepath.Abs(stored); err == nil {
		stored = abs
	}
	rel, err := filepath.Rel(s.cfg.DownloadsDir, stored)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return p // not under the container downloads root: leave as-is
	}
	mapped := filepath.Join(s.cfg.HostDownloadsDir, rel)
	return &mapped
}

// viewLesson maps a lesson to its wire shape with the on-disk paths rewritten to
// the host (see hostPath). Handlers use this instead of the bare lessonDTO so the
// output_dir/video_path the UI shows and copies resolve on the host.
func (s *Server) viewLesson(l database.Lesson) LessonDTO {
	d := lessonDTO(l)
	d.OutputDir = s.hostPath(d.OutputDir)
	d.VideoPath = s.hostPath(d.VideoPath)
	return d
}

// viewLessons is viewLesson over a slice, returning a non-nil empty slice for
// empty/nil input so the JSON array is [] rather than null.
func (s *Server) viewLessons(ls []database.Lesson) []LessonDTO {
	out := make([]LessonDTO, 0, len(ls))
	for _, l := range ls {
		out = append(out, s.viewLesson(l))
	}
	return out
}

// followDTOs maps a slice of follows to wire shapes, returning a non-nil empty
// slice for empty/nil input so the JSON array is [] rather than null.
func followDTOs(fs []database.Follow) []FollowDTO {
	out := make([]FollowDTO, 0, len(fs))
	for _, f := range fs {
		out = append(out, followDTO(f))
	}
	return out
}

// lessonDTOs maps a slice of lessons to wire shapes, returning a non-nil empty
// slice for empty/nil input so the JSON array is [] rather than null.
func lessonDTOs(ls []database.Lesson) []LessonDTO {
	out := make([]LessonDTO, 0, len(ls))
	for _, l := range ls {
		out = append(out, lessonDTO(l))
	}
	return out
}

// jobDTOs maps a slice of jobs to wire shapes, returning a non-nil empty slice
// for empty/nil input so the JSON array is [] rather than null.
func jobDTOs(js []database.Job) []JobDTO {
	out := make([]JobDTO, 0, len(js))
	for _, j := range js {
		out = append(out, jobDTO(j))
	}
	return out
}
