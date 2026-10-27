# Fix: reserved `direct`/`block` outbound tags in `/sub/{subID}` (2026-10-27)

## Problem
`tagProxyOutbounds` gave each proxy outbound its source `remark` unless that
remark was empty or already used by ANOTHER PROXY. The reserved tags were not
in the occupied set, so a node whose remark was literally `direct` or `block`
kept it verbatim. `renderXrayConnection` always appends
`{"protocol":"freedom","tag":"direct"}` and `{"protocol":"blackhole","tag":"block"}`,
and Xray resolves routing targets BY TAG NAME. Result: two outbounds shared one
tag and the profile's routing silently sent what should bypass the tunnel
(`geoip:ru`, `geosite:category-ru`, `geosite:private`) through the VPN node — or
routed the ad/IP-check block rules into the proxy. No error, wrong traffic path.

Secondary defect in the same code: the `proxy-N` fallback used `proxy-%d` with
`i+1` only, so it could itself collide with a tag another outbound already
carried (e.g. a node remarked `proxy-2`).

## Fix
`internal/subserver/subscription_handler_split.go`:

```go
var reservedOutboundTags = []string{"direct", "block"}

func tagProxyOutbounds(outbounds []json.RawMessage) error {
	used := make(map[string]struct{}, len(outbounds)+len(reservedOutboundTags))
	for _, reserved := range reservedOutboundTags {
		used[reserved] = struct{}{}
	}
	// ...
		if _, dup := used[tag]; tag == "" || dup {
			tag = freeProxyTag(i+1, used)
		}
	// ...
}

// freeProxyTag returns the first unused "proxy-N" tag at or after start.
func freeProxyTag(start int, used map[string]struct{}) string {
	for n := start; ; n++ {
		candidate := "proxy-" + strconv.Itoa(n)
		if _, taken := used[candidate]; !taken {
			return candidate
		}
	}
}
```

`reservedOutboundTags` is the single source of truth for the non-proxy tags, so
the reservation and `renderXrayConnection`'s literals cannot drift apart
silently (they are still two places; keep them in sync if a third built-in
outbound is ever added).

Because `remarks` and the catch-all `outboundTag` come from the SAME value, a
renamed node profile stays internally consistent: the client-visible name and
the default route both change together.

## Tests
`internal/subserver/xray_subscription_test.go`:
- `TestTagProxyOutboundsReservesDirectAndBlock` — both reserved tags, tag
  uniqueness, and fallback shift when `proxy-2` is already claimed (`proxy-1,
  proxy-2, node-one, proxy-4`).
- `TestBuildResponseRemarksCollidingWithReservedTags` — end-to-end through
  `buildResponse`: nodes remarked `direct`/`block` get distinct fallback tags,
  `Outbounds[1].Tag == "direct"`, `Outbounds[2].Tag == "block"`, catch-all points
  at the renamed tag.
- `TestBuildResponseReservesDirectAndBlockTags` (added in parallel) — same
  contract via a 3-node aggregate.

Verified by rollback: removing the reservation makes the tags come out as
`["direct","block","node-one","proxy-2"]` and the e2e test fails on
`proxy tag collides with the direct outbound`.

## Files
- internal/subserver/subscription_handler_split.go
- internal/subserver/xray_subscription_test.go
- doc/api.md (`GET /sub/{subID}`: free `proxy-N`, reserved tags never handed to
  a proxy outbound)

## Note on the parallel session
This work overlapped with a concurrent operator on the same tree: `origin/dev`
had already received an equivalent fix from `coderabbitai[bot]` (`76a5236`), and
`git reflog` shows the local branch being committed, reset and fast-forwarded
mid-session (`bf3d730` → `798a424` → `4df3591` → `45ecb8c`). The final pushed
state carries the `reservedOutboundTags`/`freeProxyTag` variant plus all three
tests. Before touching git on `dev`, re-read `origin/dev` — do not assume the
local HEAD is what you last saw.
