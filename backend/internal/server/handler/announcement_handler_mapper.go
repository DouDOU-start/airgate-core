package handler

import (
	"time"

	appannouncement "github.com/DouDOU-start/airgate-core/internal/app/announcement"
	"github.com/DouDOU-start/airgate-core/internal/server/dto"
)

func toAnnouncementRespFromDomain(item appannouncement.Announcement) dto.AnnouncementResp {
	return dto.AnnouncementResp{
		ID:         int64(item.ID),
		Title:      item.Title,
		Content:    item.Content,
		Status:     item.Status,
		NotifyMode: item.NotifyMode,
		StartsAt:   formatOptionalTime(item.StartsAt),
		EndsAt:     formatOptionalTime(item.EndsAt),
		TimeMixin: dto.TimeMixin{
			CreatedAt: item.CreatedAt,
			UpdatedAt: item.UpdatedAt,
		},
	}
}

func toUserAnnouncementResp(item appannouncement.UserAnnouncement) dto.UserAnnouncementResp {
	return dto.UserAnnouncementResp{
		ID:         int64(item.ID),
		Title:      item.Title,
		Content:    item.Content,
		NotifyMode: item.NotifyMode,
		ReadAt:     formatOptionalTime(item.ReadAt),
		TimeMixin: dto.TimeMixin{
			CreatedAt: item.CreatedAt,
			UpdatedAt: item.UpdatedAt,
		},
	}
}

func formatOptionalTime(t *time.Time) *string {
	if t == nil {
		return nil
	}
	formatted := t.Format(time.RFC3339)
	return &formatted
}
