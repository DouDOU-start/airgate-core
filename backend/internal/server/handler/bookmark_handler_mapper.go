package handler

import (
	appbookmark "github.com/DouDOU-start/airgate-core/internal/app/bookmark"
	"github.com/DouDOU-start/airgate-core/internal/server/dto"
)

func toBookmarkResp(item appbookmark.Bookmark) dto.BookmarkResp {
	return dto.BookmarkResp{
		ID:      int64(item.ID),
		Name:    item.Name,
		BaseURL: item.BaseURL,
		Remark:  item.Remark,
		TimeMixin: dto.TimeMixin{
			CreatedAt: item.CreatedAt,
			UpdatedAt: item.UpdatedAt,
		},
	}
}
