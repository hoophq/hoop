package apivalidation

import (
	"fmt"
	"strconv"
)

func ParsePaginationParams(pageStr, pageSizeStr string) (page int, pageSize int, err error) {
	page = 1      // Default to page 1
	pageSize = 50 // Default page size

	if pageStr != "" {
		page, err = strconv.Atoi(pageStr)
		if err != nil || page < 1 {
			return 0, 0, fmt.Errorf("page must be >= 1")
		}
	}

	if pageSizeStr != "" {
		pageSize, err = strconv.Atoi(pageSizeStr)
		if err != nil || pageSize < 1 || pageSize > 100 {
			return 0, 0, fmt.Errorf("page_size must be between 1 and 100")
		}
	}

	return page, pageSize, nil
}
