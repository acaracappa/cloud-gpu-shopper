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
| BL-006 | Medium | POST /sessions response truncated by HTTP WriteTimeout | **Fixed** | Server WriteTimeout was 60s, but Blue Lobster provisioning takes 60-114s. Go's `http.Server` silently closes the connection mid-write. Increased WriteTimeout to 300s. Root cause of "missing SSH keys" — response was cut off before the key was written. |
| BL-007 | High | Metadata not persisted by Blue Lobster API | **Mitigated** | API doesn't persist metadata. `ListAllInstances` now falls back to parsing session ID from instance `name` field via `ParseLabel()`. Deployment ID matching still won't work (provider-side limitation), but orphan detection by provider ID is unaffected. |
| BL-008 | Medium | `power_status: null` on running instances | **Fixed** | GET /instances/{id} returns null power_status. `GetInstanceStatus` and `getInstanceInfo` now infer "running" when IP is present and power_status is empty. |
| BL-009 | Low | `phl` datacenter (Philadelphia) shows no capacity | **Not a bug** | The `phl` datacenter had RTX 2080 Ti availability at design time but was unavailable during testing. Normal capacity fluctuation for a development datacenter. |
| BL-010 | High | `template_name` ignored — all instances get Ubuntu 22.04 base image | **Provider bug** | Tested `UBUNTU-24-04-NV` and `DEBIAN-12-NV` via shopper API. Both produced Ubuntu 22.04.5 LTS with NVIDIA driver 560.35.05, no Docker. The `/instances/templates` endpoint claims `has_docker: true` for 4 templates but this is inaccurate. All instances receive the same image regardless of `template_name`. Benchmark runner must install Docker post-provisioning. |

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

## Remediation Status

### Fixed in Our Code
- BL-001 through BL-005: Fixed during testing session
- BL-006: WriteTimeout increased to 300s (was truncating responses)
- BL-007: Mitigated with name-based session ID fallback
- BL-008: Fixed with IP-based status inference

### Provider-Side Issues
- BL-009: Not a bug (capacity fluctuation)
- BL-010: `template_name` ignored by API — all instances get Ubuntu 22.04 base. Benchmark runner needs post-provisioning Docker install.
