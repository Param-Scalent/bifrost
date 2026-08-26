package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	bifrost "github.com/maximhq/bifrost/core"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
	configstoreTables "github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/maximhq/bifrost/transports/bifrost-http/lib"
	"github.com/valyala/fasthttp"
)

type listModelsTestAccount struct {
	configs map[schemas.ModelProvider]*schemas.ProviderConfig
	keys    map[schemas.ModelProvider][]schemas.Key
}

func (a *listModelsTestAccount) GetConfiguredProviders() ([]schemas.ModelProvider, error) {
	providers := make([]schemas.ModelProvider, 0, len(a.configs))
	for provider := range a.configs {
		providers = append(providers, provider)
	}
	return providers, nil
}

func (a *listModelsTestAccount) GetKeysForProvider(_ context.Context, provider schemas.ModelProvider) ([]schemas.Key, error) {
	return a.keys[provider], nil
}

func (a *listModelsTestAccount) GetConfigForProvider(provider schemas.ModelProvider) (*schemas.ProviderConfig, error) {
	return a.configs[provider], nil
}

type mockListModelsVKConfigStore struct {
	configstore.ConfigStore
	vk  *configstoreTables.TableVirtualKey
	err error
}

func (m *mockListModelsVKConfigStore) GetVirtualKeyByValue(_ context.Context, _ string) (*configstoreTables.TableVirtualKey, error) {
	return m.vk, m.err
}

func TestApplyListModelsVirtualKeyProviderFilterSetsActiveVKProviders(t *testing.T) {
	h := &CompletionHandler{
		config: &lib.Config{
			ConfigStore: &mockListModelsVKConfigStore{vk: &configstoreTables.TableVirtualKey{
				Value:    *schemas.NewSecretVar("sk-bf-active"),
				IsActive: new(true),
				ProviderConfigs: []configstoreTables.TableVirtualKeyProviderConfig{
					{Provider: "openai"},
					{Provider: " anthropic "},
					{Provider: ""},
				},
			}},
		},
	}

	ctx := &fasthttp.RequestCtx{}
	ctx.Request.Header.Set("Authorization", "Bearer sk-bf-active")
	bifrostCtx := schemas.NewBifrostContext(context.Background(), time.Time{})

	if ok := h.applyListModelsVirtualKeyProviderFilter(ctx, bifrostCtx); !ok {
		t.Fatalf("expected active VK to apply provider filter")
	}
	got, ok := bifrostCtx.Value(schemas.BifrostContextKeyAvailableProviders).([]schemas.ModelProvider)
	if !ok {
		t.Fatalf("expected available providers to be set")
	}
	want := []schemas.ModelProvider{schemas.OpenAI, schemas.Anthropic}
	if len(got) != len(want) {
		t.Fatalf("expected providers %#v, got %#v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("expected providers %#v, got %#v", want, got)
		}
	}
}

func TestApplyListModelsVirtualKeyProviderFilterReturnsErrorOnLookupFailure(t *testing.T) {
	h := &CompletionHandler{
		config: &lib.Config{
			ConfigStore: &mockListModelsVKConfigStore{err: errors.New("database unavailable")},
		},
	}

	ctx := &fasthttp.RequestCtx{}
	ctx.Request.Header.Set("Authorization", "Bearer sk-bf-active")
	bifrostCtx := schemas.NewBifrostContext(context.Background(), time.Time{})

	if ok := h.applyListModelsVirtualKeyProviderFilter(ctx, bifrostCtx); ok {
		t.Fatalf("expected lookup error to fail request")
	}
	if got := ctx.Response.StatusCode(); got != fasthttp.StatusInternalServerError {
		t.Fatalf("expected status %d, got %d", fasthttp.StatusInternalServerError, got)
	}
	if body := string(ctx.Response.Body()); !strings.Contains(body, "Failed to resolve virtual key") {
		t.Fatalf("expected virtual key lookup error response, got %q", body)
	}
}

func TestApplyListModelsVirtualKeyProviderFilterReturnsUnavailableWithoutConfigStore(t *testing.T) {
	h := &CompletionHandler{config: &lib.Config{}}

	ctx := &fasthttp.RequestCtx{}
	ctx.Request.Header.Set("Authorization", "Bearer sk-bf-active")
	bifrostCtx := schemas.NewBifrostContext(context.Background(), time.Time{})

	if ok := h.applyListModelsVirtualKeyProviderFilter(ctx, bifrostCtx); ok {
		t.Fatalf("expected missing config store to fail request")
	}
	if got := ctx.Response.StatusCode(); got != fasthttp.StatusServiceUnavailable {
		t.Fatalf("expected status %d, got %d", fasthttp.StatusServiceUnavailable, got)
	}
	if body := string(ctx.Response.Body()); !strings.Contains(body, "database store unavailable") {
		t.Fatalf("expected unavailable response, got %q", body)
	}
}

func TestApplyListModelsVirtualKeyProviderFilterSkipsWhenVKNotFound(t *testing.T) {
	h := &CompletionHandler{
		config: &lib.Config{
			ConfigStore: &mockListModelsVKConfigStore{},
		},
	}

	ctx := &fasthttp.RequestCtx{}
	ctx.Request.Header.Set("Authorization", "Bearer sk-bf-missing")
	bifrostCtx := schemas.NewBifrostContext(context.Background(), time.Time{})

	if ok := h.applyListModelsVirtualKeyProviderFilter(ctx, bifrostCtx); !ok {
		t.Fatalf("expected missing VK to be ignored without failing request")
	}
	if got := bifrostCtx.Value(schemas.BifrostContextKeyAvailableProviders); got != nil {
		t.Fatalf("expected missing VK not to set available providers, got %#v", got)
	}
}

func TestApplyListModelsVirtualKeyProviderFilterSkipsInactiveVK(t *testing.T) {
	h := &CompletionHandler{
		config: &lib.Config{
			ConfigStore: &mockListModelsVKConfigStore{vk: &configstoreTables.TableVirtualKey{
				Value:    *schemas.NewSecretVar("sk-bf-inactive"),
				IsActive: new(false),
				ProviderConfigs: []configstoreTables.TableVirtualKeyProviderConfig{
					{Provider: "openai"},
				},
			}},
		},
	}

	ctx := &fasthttp.RequestCtx{}
	ctx.Request.Header.Set("Authorization", "Bearer sk-bf-inactive")
	bifrostCtx := schemas.NewBifrostContext(context.Background(), time.Time{})

	if ok := h.applyListModelsVirtualKeyProviderFilter(ctx, bifrostCtx); !ok {
		t.Fatalf("expected inactive VK to be ignored without failing request")
	}
	if got := bifrostCtx.Value(schemas.BifrostContextKeyAvailableProviders); got != nil {
		t.Fatalf("expected inactive VK not to set available providers, got %#v", got)
	}
}

// TestListModels_VKOnlyAllowedProviderReturnsAllowedModels verifies that the aggregate endpoint returns only models from providers allowed by the VK.
func TestListModels_VKOnlyAllowedProviderReturnsAllowedModels(t *testing.T) {
	openRouterRequests := 0
	openRouterServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		openRouterRequests++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"openrouter-test-model","object":"model","created":1,"owned_by":"openrouter"}]}`))
	}))
	defer openRouterServer.Close()

	anthropicRequests := 0
	anthropicServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		anthropicRequests++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"anthropic-must-not-appear","object":"model"}]}`))
	}))
	defer anthropicServer.Close()

	account := &listModelsTestAccount{
		configs: map[schemas.ModelProvider]*schemas.ProviderConfig{
			schemas.OpenRouter: {
				NetworkConfig: schemas.NetworkConfig{BaseURL: openRouterServer.URL},
			},
			schemas.Anthropic: {
				NetworkConfig: schemas.NetworkConfig{BaseURL: anthropicServer.URL},
			},
		},
		keys: map[schemas.ModelProvider][]schemas.Key{
			schemas.OpenRouter: {{ID: "openrouter-key", Value: *schemas.NewSecretVar("sk-openrouter-test"), Models: schemas.WhiteList{"*"}, Weight: 1}},
			schemas.Anthropic:  {{ID: "anthropic-key", Value: *schemas.NewSecretVar("sk-anthropic-test"), Models: schemas.WhiteList{"*"}, Weight: 1}},
		},
	}

	client, err := bifrost.Init(context.Background(), schemas.BifrostConfig{Account: account})
	if err != nil {
		t.Fatalf("failed to initialize Bifrost: %v", err)
	}
	defer client.Shutdown()

	configStore := &mockListModelsVKConfigStore{vk: &configstoreTables.TableVirtualKey{
		Value:    *schemas.NewSecretVar("sk-bf-openrouter-only"),
		IsActive: new(true),
		ProviderConfigs: []configstoreTables.TableVirtualKeyProviderConfig{
			{Provider: string(schemas.OpenRouter), AllowedModels: schemas.WhiteList{"*"}},
		},
	}}
	h := NewInferenceHandler(client, &lib.Config{
		ConfigStore:  configStore,
		ClientConfig: new(configstore.ClientConfig),
	})

	ctx := &fasthttp.RequestCtx{}
	ctx.Request.Header.SetMethod("GET")
	ctx.Request.SetRequestURI("/v1/models")
	ctx.Request.Header.Set("Authorization", "Bearer sk-bf-openrouter-only")
	h.listModels(ctx)

	if got := ctx.Response.StatusCode(); got != fasthttp.StatusOK {
		t.Fatalf("expected HTTP 200, got %d: %s", got, ctx.Response.Body())
	}
	body := string(ctx.Response.Body())
	if !strings.Contains(body, "openrouter-test-model") {
		t.Fatalf("expected OpenRouter model in response, got %s", body)
	}
	if strings.Contains(body, "anthropic-must-not-appear") {
		t.Fatalf("Anthropic model unexpectedly appeared in response: %s", body)
	}
	if openRouterRequests == 0 {
		t.Fatal("expected OpenRouter server to receive a list-model request")
	}
	if anthropicRequests != 0 {
		t.Fatalf("expected Anthropic server to receive no requests, got %d", anthropicRequests)
	}
}
