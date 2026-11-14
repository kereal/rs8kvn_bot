package admin

import (
	"net/http"
	"strconv"
)

// listParams is the pagination/sort core shared by every admin list screen:
// limit, offset, whitelisted sort column and order direction. Screen-specific
// filters stay in their own parse*Filter functions and copy the parsed core in.
type listParams struct {
	Limit  int
	Offset int
	Sort   string
	Desc   bool
}

// parseListParams parses the shared limit/offset/sort/order query contract:
// limit is a positive integer clamped to maxLimit (P7), offset is
// non-negative, sort must be in the whitelist (P8), order defaults to desc.
// Violations and malformed values answer bad_request, never silent.
func parseListParams(w http.ResponseWriter, r *http.Request, defaultLimit, maxLimit int, sortColumns map[string]bool) (listParams, bool) {
	p := listParams{Limit: defaultLimit, Desc: true}
	query := r.URL.Query()

	if raw := query.Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit <= 0 {
			writeError(w, http.StatusBadRequest, "bad_request", "limit must be a positive integer")
			return p, false
		}
		p.Limit = min(limit, maxLimit)
	}

	if raw := query.Get("offset"); raw != "" {
		offset, err := strconv.Atoi(raw)
		if err != nil || offset < 0 {
			writeError(w, http.StatusBadRequest, "bad_request", "offset must be a non-negative integer")
			return p, false
		}
		p.Offset = offset
	}

	if raw := query.Get("sort"); raw != "" {
		if !sortColumns[raw] {
			writeError(w, http.StatusBadRequest, "bad_request", "unknown sort column")
			return p, false
		}
		p.Sort = raw
	}

	switch raw := query.Get("order"); raw {
	case "", "desc":
		p.Desc = true
	case "asc":
		p.Desc = false
	default:
		writeError(w, http.StatusBadRequest, "bad_request", "order must be asc or desc")
		return p, false
	}

	return p, true
}
