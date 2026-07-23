package api

const maxPageSize = 1000

func saturatedPageStart(page, limit int) int {
	maximum := int(^uint(0) >> 1)
	if page <= 0 || limit <= 0 || page-1 > maximum/limit {
		return maximum
	}
	return (page - 1) * limit
}

func pageBounds(total, page, limit int) (int, int) {
	if total <= 0 || page < 1 || limit < 1 {
		return 0, 0
	}

	start := saturatedPageStart(page, limit)
	if start > total {
		return total, total
	}
	if start >= total {
		return total, total
	}
	end := start + min(limit, total-start)
	return start, end
}
