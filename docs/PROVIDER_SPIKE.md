# Provider API Spike Results

**Date**: 2025-01-29

## Vast.ai

### API Details
- **Base URL**: `https://console.vast.ai/api/v0`
- **Auth**: Bearer token in Authorization header
- **Docs**: https://vast.ai/docs/api/commands

### Endpoints Tested

| Endpoint | Method | Status | Notes |
|----------|--------|--------|-------|
| `/bundles/` | GET | Working | Lists available GPU offers. Note trailing slash required. |
| `/instances/` | GET | Not tested | Lists user's instances |
| `/asks/` | POST | Not tested | Creates a rental |

### Key Response Fields (bundles)
```json
{
  "id": 30406190,
  "gpu_name": "RTX 5090",
  "gpu_ram": 32607,
  "num_gpus": 1,
  "dph_total": 0.2288,  // dollars per hour total
  "geolocation": ", CN",
  "reliability2": 0.9941378,
  "rentable": true,
  "rented": false,
  "cpu_name": "AMD EPYC 7K62 48-Core Processor",
  "disk_space": 505.5
}
```

### Auth Example
```bash
curl -H "Authorization: Bearer $VASTAI_API_KEY" \
  "https://console.vast.ai/api/v0/bundles/?q=%7B%22rentable%22%3A%7B%22eq%22%3Atrue%7D%7D"
```

---

## TensorDock

### API Details
- **Base URL**: `https://dashboard.tensordock.com/api/v2` (NOT marketplace.tensordock.com)
- **Auth**: Query parameters `api_key` and `api_token`
- **Docs**: https://dashboard.tensordock.com/api/docs

### Important Notes
- The older marketplace API (`marketplace.tensordock.com/api/v0`) has server-side bugs
- Use the v2 API on `dashboard.tensordock.com` instead
- Auth uses `api_key` (the Authorization ID) and `api_token`

### Endpoints Tested

| Endpoint | Method | Status | Notes |
|----------|--------|--------|-------|
| `/locations` | GET | Working | Lists locations with available GPUs and pricing |
| `/instances` | GET | Auth issue | May require different auth or empty response |

### Key Response Fields (locations)
```json
{
  "data": {
    "locations": [
      {
        "id": "8dd95ff0-dff9-489c-9b02-b38a366b6ab1",
        "city": "Chubbuck",
        "stateprovince": "Idaho",
        "country": "United States",
        "tier": 1,
        "gpus": [
          {
            "v0Name": "geforcertx4090-pcie-24gb",
            "displayName": "NVIDIA GeForce RTX 4090 PCIe 24GB",
            "max_count": 4,
            "price_per_hr": 0.3966,
            "resources": {
              "max_vcpus": 56,
              "max_ram_gb": 331,
              "max_storage_gb": 30200
            }
          }
        ]
      }
    ]
  }
}
```

### Auth Example
```bash
curl "https://dashboard.tensordock.com/api/v2/locations?api_key=$TENSORDOCK_AUTH_ID&api_token=$TENSORDOCK_API_TOKEN"
```

---

## Implementation Notes

### Vast.ai Adapter
1. Use Bearer token auth
2. Query `/bundles/` with trailing slash
3. Parse `dph_total` for price, `reliability2` for reliability
4. Use `id` as ProviderID
5. Instance tagging: Use instance label/name with "shopper-{sessionID}" prefix

### TensorDock Adapter
1. Use query param auth (`api_key`, `api_token`)
2. Query `/locations` to get available GPUs by location
3. Location-based deployment uses location_id
4. Instance tagging: Use VM name with "shopper-{sessionID}" prefix
5. v2 API uses different create instance format (see docs)

### Rate Limiting
- Vast.ai: Has rate limits, implement adaptive caching
- TensorDock: "Request keeping API requests reasonable (e.g., 1 request per second)"

### GPU Name Normalization
Map provider-specific names to common names:
- `geforcertx4090-pcie-24gb` → `RTX 4090`
- `RTX 5090` → `RTX 5090`
- etc.
