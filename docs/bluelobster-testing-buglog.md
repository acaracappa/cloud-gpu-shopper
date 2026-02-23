# Blue Lobster Provider — Live Testing Bug Log

**Date**: 2026-02-23
**Branch**: `feat/provider-bluelobster`
**Tester**: Claude + avc

## Test Plan

Provision 3 Blue Lobster GPU instances via our API to validate the full lifecycle:
1. RTX A4000 x1 — `igl` datacenter (prod), $0.20/hr
2. RTX A5000 x1 — `igl` datacenter (prod), $0.30/hr
3. RTX A6000 x1 — `igl` datacenter (prod), $0.55/hr

**Testing**: Provisioning, SSH connectivity, GPU verification, session lifecycle, teardown.

---

## Session Results

| Session | GPU | IP | Provision Time | CUDA | SSH | Destroy |
|---------|-----|-----|----------------|------|-----|---------|
| d9bb6873 | RTX A4000 x1 | 38.29.145.235 | ~114s | 12.6 / 560.35.05 | OK (server-verified) | OK |
| 48f45c18 | RTX A5000 x1 | 38.29.145.237 | ~76s | 12.6 / 560.35.05 | OK (server-verified) | OK |
| aaedc6bc | RTX A6000 x1 | 38.29.145.238 | ~63s | 12.6 / 560.35.05 | OK (server-verified) | OK |

All 3 instances provisioned successfully, CUDA verified, SSH reachable, cleanly destroyed.

---

## Bug Log

| # | Severity | Summary | Status | Fix |
|---|----------|---------|--------|-----|
| BL-001 | Medium | VRAM returns 0 for all offers | **Fixed** | GPU model lookup table instead of description parsing. API `gpu_description` is "N/A", `memory_gib` is system RAM not VRAM. |
| BL-002 | High | Name validation: `ToLabel()` can produce trailing hyphen | **Fixed** | Added `sanitizeInstanceName()` that strips invalid chars and ensures `^[a-zA-Z0-9][a-zA-Z0-9-]*[a-zA-Z0-9]$` compliance. |
| BL-003 | High | Launch response format mismatch — fields at top level, not in `data` wrapper | **Fixed** | Changed `LaunchInstanceResponse` to flat struct with `task_id`, `vm_uuid`, `assigned_ip`, `status`. Updated all references from `launchResp.Data.X` to `launchResp.X`. |
| BL-004 | High | SSH public key trailing newline rejected by API regex | **Fixed** | Added `strings.TrimSpace(req.SSHPublicKey)` before sending to Blue Lobster. `ssh.MarshalAuthorizedKey()` adds `\n` which fails their regex `$`. |
| BL-005 | Low | Error responses missing field-level detail | **Fixed** | Added `FieldError` type and `Errors []FieldError` to `ErrorResponse`. Parser now appends `[field: message]` to error string. |
| BL-006 | Info | POST /sessions response empty on client side (curl exit 52) | **Not a bug** | CreateInstance blocks for ~1-2 min during task polling. Long-lived HTTP request + curl default timeout = empty reply. This affects all providers equally. Use `--max-time 240` for curl. |
| BL-007 | High | Metadata not persisted by Blue Lobster API | **Open** | We send `metadata` map in launch request, but GET /instances returns `metadata: null`. This breaks orphan detection and reconciliation tag matching. **Workaround**: Session ID is embedded in the instance `name` field (`shopper-{session_id}`), so we can parse that as a fallback for orphan matching. |
| BL-008 | Medium | `power_status: null` on running instances | **Open** | GET /instances/{id} returns `power_status: null` even when instance is running. GET /instances (list) returns `power_status: "running"`. Our `GetInstanceStatus` should handle null gracefully. |
| BL-009 | Low | `phl` datacenter (Philadelphia) shows no capacity | **Not a bug** | The `phl` datacenter had RTX 2080 Ti availability at design time but was unavailable during testing. Normal capacity fluctuation for a development datacenter. |

---

## Positive Findings

- **Provisioning works end-to-end**: All 3 instances created, CUDA verified, SSH available
- **Clean teardown**: All 3 instances destroyed cleanly via our API
- **CUDA 12.6 consistent**: All instances had identical CUDA 12.6 / Driver 560.35.05
- **SSH available**: Server-side SSH verification passed on all 3 instances
- **Pricing correct**: $0.20/hr, $0.30/hr, $0.55/hr all match Blue Lobster inventory
- **Name-based tracking works**: Instance names embed our session ID for reconciliation
- **Fast provisioning**: 63-114 seconds from request to running instance

---

## Remediation Priority

### Must Fix Before Merge
1. **BL-007** — Orphan detection via name field fallback (metadata not persisted)
2. **BL-008** — Handle null power_status gracefully

### Fixed During Testing
- BL-001 through BL-005 all fixed and committed
