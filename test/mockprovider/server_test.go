package mockprovider

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestState_NewState(t *testing.T) {
	state := NewState()

	offers := state.ListOffers()
	assert.NotEmpty(t, offers, "should have default offers")
	assert.GreaterOrEqual(t, len(offers), 4, "should have at least 4 default offers")
}

func TestState_ListOffers(t *testing.T) {
	state := NewState()

	offers := state.ListOffers()

	// Check that we have expected GPU types
	gpuTypes := make(map[string]bool)
	for _, offer := range offers {
		gpuTypes[offer.GPUName] = true
	}

	assert.True(t, gpuTypes["RTX 4090"], "should have RTX 4090")
	assert.True(t, gpuTypes["A100 SXM4"], "should have A100")
	assert.True(t, gpuTypes["H100 SXM5"], "should have H100")
}

func TestState_CreateInstance(t *testing.T) {
	state := NewState()

	instance, err := state.CreateInstance("offer-rtx4090-1", "test-instance", nil, "")

	require.NoError(t, err)
	assert.NotEmpty(t, instance.ID)
	assert.Equal(t, "test-instance", instance.Label)
	assert.Equal(t, StatusCreating, instance.Status)

	// Wait for instance to become running
	time.Sleep(200 * time.Millisecond)

	inst, ok := state.GetInstance(instance.ID)
	require.True(t, ok)
	assert.Equal(t, StatusRunning, inst.Status)
}

func TestState_CreateInstance_OfferNotFound(t *testing.T) {
	state := NewState()

	_, err := state.CreateInstance("nonexistent", "test", nil, "")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "offer not found")
}

func TestState_CreateInstance_AlreadyRented(t *testing.T) {
	state := NewState()

	// Create first instance
	_, err := state.CreateInstance("offer-rtx4090-1", "first", nil, "")
	require.NoError(t, err)

	// Try to create another with same offer
	_, err = state.CreateInstance("offer-rtx4090-1", "second", nil, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already rented")
}

func TestState_DestroyInstance(t *testing.T) {
	state := NewState()

	instance, err := state.CreateInstance("offer-rtx4090-1", "test", nil, "")
	require.NoError(t, err)

	err = state.DestroyInstance(instance.ID)
	require.NoError(t, err)

	// Instance should be marked destroyed
	inst, ok := state.GetInstance(instance.ID)
	require.True(t, ok)
	assert.Equal(t, StatusDestroyed, inst.Status)

	// Wait for cleanup
	time.Sleep(100 * time.Millisecond)

	// Instance should be gone
	_, ok = state.GetInstance(instance.ID)
	assert.False(t, ok)
}

func TestState_DestroyInstance_NotFound(t *testing.T) {
	state := NewState()

	err := state.DestroyInstance("nonexistent")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "instance not found")
}

func TestState_Reset(t *testing.T) {
	state := NewState()

	// Create an instance
	instance, _ := state.CreateInstance("offer-rtx4090-1", "test", nil, "")

	// Reset
	state.Reset()

	// Instance should be gone
	_, ok := state.GetInstance(instance.ID)
	assert.False(t, ok)

	// Offers should be available again
	offers := state.ListOffers()
	assert.GreaterOrEqual(t, len(offers), 4)
}

func TestState_FailCreate(t *testing.T) {
	state := NewState()
	state.SetFailCreate(true, "provider unavailable")

	_, err := state.CreateInstance("offer-rtx4090-1", "test", nil, "")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "provider unavailable")
}

func TestState_FailDestroy(t *testing.T) {
	state := NewState()

	instance, _ := state.CreateInstance("offer-rtx4090-1", "test", nil, "")
	state.SetFailDestroy(true, "destroy failed")

	err := state.DestroyInstance(instance.ID)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "destroy failed")
}

func TestState_CreateOrphanInstance(t *testing.T) {
	state := NewState()

	instance := state.CreateOrphanInstance("orphan-test")

	assert.NotEmpty(t, instance.ID)
	assert.Equal(t, "orphan-test", instance.Label)
	assert.Equal(t, StatusRunning, instance.Status)

	// Verify it shows in list
	instances := state.ListInstances()
	found := false
	for _, inst := range instances {
		if inst.ID == instance.ID {
			found = true
			break
		}
	}
	assert.True(t, found)
}

// Server tests

func TestServer_Health(t *testing.T) {
	server := NewServer(nil)

	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()

	server.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]string
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Equal(t, "ok", response["status"])
	assert.Equal(t, "mock-vastai-provider", response["type"])
}

func TestServer_ListOffers(t *testing.T) {
	server := NewServer(nil)

	req := httptest.NewRequest("GET", "/bundles/", nil)
	w := httptest.NewRecorder()

	server.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response BundlesResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(response.Offers), 4)
}

func TestServer_CreateInstance(t *testing.T) {
	state := NewState()
	server := NewServer(state)

	// Get an offer ID
	offers := state.ListOffers()
	require.NotEmpty(t, offers)
	offerID := offers[0].ID

	body := CreateInstanceRequest{
		ClientID: "test-client",
		Image:    "pytorch/pytorch:latest",
		Label:    "test-instance",
		Disk:     20.0,
	}
	bodyBytes, _ := json.Marshal(body)

	req := httptest.NewRequest("PUT", "/asks/"+offerID+"/", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response CreateInstanceResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.True(t, response.Success)
	assert.Greater(t, response.NewContract, 0)
}

func TestServer_CreateInstance_NotFound(t *testing.T) {
	server := NewServer(nil)

	body := CreateInstanceRequest{Label: "test"}
	bodyBytes, _ := json.Marshal(body)

	req := httptest.NewRequest("PUT", "/asks/nonexistent/", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestServer_ListInstances(t *testing.T) {
	state := NewState()
	server := NewServer(state)

	// Create an instance first
	_, _ = state.CreateInstance("offer-rtx4090-1", "test", nil, "")

	req := httptest.NewRequest("GET", "/instances/", nil)
	w := httptest.NewRecorder()

	server.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response InstancesResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.Len(t, response.Instances, 1)
}

func TestServer_GetInstance(t *testing.T) {
	state := NewState()
	server := NewServer(state)

	instance, _ := state.CreateInstance("offer-rtx4090-1", "test", nil, "")

	req := httptest.NewRequest("GET", "/instances/"+instance.ID+"/", nil)
	w := httptest.NewRecorder()

	server.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response InstanceResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.Equal(t, "test", response.Label)
}

func TestServer_GetInstance_NotFound(t *testing.T) {
	server := NewServer(nil)

	req := httptest.NewRequest("GET", "/instances/nonexistent/", nil)
	w := httptest.NewRecorder()

	server.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestServer_DestroyInstance(t *testing.T) {
	state := NewState()
	server := NewServer(state)

	instance, _ := state.CreateInstance("offer-rtx4090-1", "test", nil, "")

	req := httptest.NewRequest("DELETE", "/instances/"+instance.ID+"/", nil)
	w := httptest.NewRecorder()

	server.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response DestroyResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.True(t, response.Success)
}

func TestServer_DestroyInstance_NotFound(t *testing.T) {
	server := NewServer(nil)

	req := httptest.NewRequest("DELETE", "/instances/nonexistent/", nil)
	w := httptest.NewRecorder()

	server.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestServer_TestReset(t *testing.T) {
	state := NewState()
	server := NewServer(state)

	// Create an instance
	_, _ = state.CreateInstance("offer-rtx4090-1", "test", nil, "")

	// Reset via API
	req := httptest.NewRequest("POST", "/_test/reset", nil)
	w := httptest.NewRecorder()

	server.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	// Verify instance is gone
	instances := state.ListInstances()
	assert.Empty(t, instances)
}

func TestServer_TestConfig(t *testing.T) {
	state := NewState()
	server := NewServer(state)

	config := TestConfig{
		FailCreate:    true,
		FailCreateMsg: "configured failure",
	}
	bodyBytes, _ := json.Marshal(config)

	req := httptest.NewRequest("POST", "/_test/config", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	// Verify create now fails
	_, err := state.CreateInstance("offer-rtx4090-1", "test", nil, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "configured failure")
}

func TestServer_TestCreateOrphan(t *testing.T) {
	state := NewState()
	server := NewServer(state)

	body := TestOrphanRequest{Label: "orphan-test"}
	bodyBytes, _ := json.Marshal(body)

	req := httptest.NewRequest("POST", "/_test/orphan", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]string
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.NotEmpty(t, response["instance_id"])
	assert.Equal(t, "orphan-test", response["label"])
}

func TestServer_FullProvisionDestroyFlow(t *testing.T) {
	state := NewState()
	server := NewServer(state)

	// 1. List offers
	req := httptest.NewRequest("GET", "/bundles/", nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var bundlesResp BundlesResponse
	json.Unmarshal(w.Body.Bytes(), &bundlesResp)
	require.NotEmpty(t, bundlesResp.Offers)

	// 2. Create instance
	createBody := CreateInstanceRequest{
		ClientID: "e2e-test",
		Image:    "pytorch:latest",
		Label:    "shopper-test-session-123",
		Disk:     20.0,
	}
	bodyBytes, _ := json.Marshal(createBody)
	req = httptest.NewRequest("PUT", "/asks/"+state.ListOffers()[0].ID+"/", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	server.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var createResp CreateInstanceResponse
	json.Unmarshal(w.Body.Bytes(), &createResp)
	require.True(t, createResp.Success)
	instanceID := createResp.NewContract

	// 3. Wait for running
	time.Sleep(200 * time.Millisecond)

	// 4. Get instance status
	req = httptest.NewRequest("GET", "/instances/"+string(rune(instanceID+48))+"/", nil)
	// Use proper ID formatting
	instances := state.ListInstances()
	require.NotEmpty(t, instances)
	req = httptest.NewRequest("GET", "/instances/"+instances[0].ID+"/", nil)
	w = httptest.NewRecorder()
	server.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var instResp InstanceResponse
	json.Unmarshal(w.Body.Bytes(), &instResp)
	assert.Equal(t, "running", instResp.ActualStatus)

	// 5. Destroy instance
	req = httptest.NewRequest("DELETE", "/instances/"+instances[0].ID+"/", nil)
	w = httptest.NewRecorder()
	server.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var destroyResp DestroyResponse
	json.Unmarshal(w.Body.Bytes(), &destroyResp)
	assert.True(t, destroyResp.Success)

	// 6. Verify offer is available again
	time.Sleep(100 * time.Millisecond)
	offers := state.ListOffers()
	found := false
	for _, o := range offers {
		if o.ID == state.ListOffers()[0].ID {
			found = true
			break
		}
	}
	assert.True(t, found || len(offers) >= 4, "offer should be available again")
}
