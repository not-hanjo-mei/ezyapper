// Package app wires the bot's subsystems together: it builds them from a
// config at startup and provides the hot reload components that rebuild
// them when the config file changes.
package app

import (
	"context"
	"fmt"

	"ezyapper/internal/ai"
	"ezyapper/internal/ai/tools"
	"ezyapper/internal/config"
	"ezyapper/internal/hotreload"
	"ezyapper/internal/logger"
	"ezyapper/internal/media"
	"ezyapper/internal/memory"
)

// memoryInputs is everything the memory service is built from, with the
// fallbacks to core.ai already resolved. Comparing two of these tells hot
// reload exactly what the memory subsystem needs, including changes that
// only reach it through a fallback (core.ai.api_key used when
// memory_pipeline.embedding.api_key is empty, and so on).
type memoryInputs struct {
	Enabled         bool
	EmbeddingModel  string
	EmbeddingClient config.AIConfig
	Qdrant          config.QdrantConfig
	Consolidation   config.AIConfig
	// ImageAI is what describes images in consolidated messages: the core
	// LLM settings plus the image modality, with consolidation.vision
	// overrides applied. Only those fields are kept, so unrelated core.ai
	// edits (the chat prompt, other modalities) do not rebuild memory.
	ImageAI          config.AIConfig
	Service          memory.ServiceConfig
	ConsolidationCfg config.ConsolidationConfig
}

// comparable returns in with the embedding client reduced to the fields an
// embedding request uses (the client config is a copy of core.ai, so it
// also carries the chat prompt, vision settings, ...; a chat prompt edit
// must not look like an embedding change).
func (in memoryInputs) comparable() memoryInputs {
	e := in.EmbeddingClient
	in.EmbeddingClient = config.AIConfig{LLMConfig: config.LLMConfig{
		APIBaseURL:  e.APIBaseURL,
		APIKey:      e.APIKey,
		RetryCount:  e.RetryCount,
		Timeout:     e.Timeout,
		ExtraParams: e.ExtraParams,
	}}
	return in
}

func memoryInputsFor(cfg *config.Config) memoryInputs {
	in := memoryInputs{
		Enabled: cfg.Memory.Retrieval.TopK > 0 || cfg.Memory.Consolidation.Enabled,
	}
	if !in.Enabled {
		return in
	}
	in.EmbeddingModel = cfg.Embedding.Model
	in.EmbeddingClient = *buildEmbeddingAIConfig(cfg)
	in.Qdrant = cfg.Qdrant
	cons := buildConsolidationAIConfig(cfg)
	in.Consolidation = *cons
	in.ImageAI = buildConsolidationImageAI(cfg)
	in.ConsolidationCfg = cfg.Memory.Consolidation
	in.Service = buildServiceConfig(cfg)
	in.Service.Consolidation = nil // compared through ConsolidationCfg
	return in
}

// BuildMemoryService builds the memory service for cfg: the no-op service
// when memory is disabled, otherwise a Qdrant-backed service (which
// connects, prepares the tiered collections and starts re-embedding).
// mediaCache is the bot's media cache (may be nil): the consolidation image
// describer reads and fills it, so images the reply path described are not
// described again.
func BuildMemoryService(cfg *config.Config, mediaCache *media.Cache) (memory.Service, error) {
	in := memoryInputsFor(cfg)
	if !in.Enabled {
		logger.Info("[main] Memory subsystem is disabled by config (retrieval.top_k=0 and consolidation.enabled=false)")
		return memory.NewNoopService(), nil
	}

	embedder, err := buildEmbedder(in)
	if err != nil {
		return nil, err
	}

	consolidationAIClient := ai.NewClient(&in.Consolidation, tools.NewToolRegistry())
	// Images in consolidated messages are described by the image modality,
	// optionally routed to another model by consolidation.vision.
	var imageDescriber memory.ImageDescriber
	if in.ImageAI.Modalities.Image.Enabled {
		imgCfg := &config.Config{}
		imgCfg.AI = in.ImageAI
		if d := media.New(imgCfg, mediaCache).ImageDescriber(); d != nil {
			imageDescriber = d
		}
	}

	qdrantClient, err := memory.NewQdrantClient(context.Background(), &in.Qdrant, in.EmbeddingModel)
	if err != nil {
		embedder.Stop()
		return nil, fmt.Errorf("failed to create qdrant client: %w", err)
	}

	svcCfg := in.Service
	cons := in.ConsolidationCfg
	svcCfg.Consolidation = &cons
	svc, err := memory.NewService(&svcCfg, &in.Qdrant, qdrantClient, embedder, consolidationAIClient, imageDescriber)
	if err != nil {
		embedder.Stop()
		_ = qdrantClient.Close()
		return nil, err
	}
	return svc, nil
}

func buildEmbedder(in memoryInputs) (memory.Embedder, error) {
	embedderClient := ai.NewClient(&in.EmbeddingClient, tools.NewToolRegistry())
	embedder, err := memory.NewAIEmbedder(embedderClient, in.EmbeddingModel)
	if err != nil {
		return nil, fmt.Errorf("failed to create embedder: %w", err)
	}
	return embedder, nil
}

// MemoryComponent rebuilds the memory subsystem behind a memory.Swappable.
type MemoryComponent struct {
	Proxy *memory.Swappable
	// build is BuildMemoryService (replaceable in tests).
	build func(*config.Config) (memory.Service, error)
}

// NewMemoryComponent returns the hot reload component for proxy. Rebuilt
// services share mediaCache with the bot (see BuildMemoryService).
func NewMemoryComponent(proxy *memory.Swappable, mediaCache *media.Cache) *MemoryComponent {
	return &MemoryComponent{Proxy: proxy, build: func(cfg *config.Config) (memory.Service, error) {
		return BuildMemoryService(cfg, mediaCache)
	}}
}

func (*MemoryComponent) Name() string { return "memory" }

// Prepare decides from the resolved inputs:
//
//   - nothing the memory service uses changed: nothing to do;
//   - only the embedding endpoint/key/timeout/retries/extra params changed
//     (same model, same everything else): a new embedder is swapped into the
//     running service, which keeps its counters and connection;
//   - anything else (on/off, Qdrant connection or retry policy, embedding
//     model or vector size, consolidation LLM or vision, any memory.*
//     setting baked into the service): a complete new service is built
//     first. For a new embedding model that selects the model's vector
//     collection and starts re-embedding it from the source tier. The
//     running service keeps serving until Commit and is closed after a
//     grace period. A connection or build error rejects the reload.
func (m *MemoryComponent) Prepare(_ context.Context, old, next *config.Config, _ hotreload.Changes) (hotreload.Pending, error) {
	oi, ni := memoryInputsFor(old), memoryInputsFor(next)
	if hotreload.Equal(oi.comparable(), ni.comparable()) {
		return nil, nil
	}

	embedderOnly := oi.Enabled && ni.Enabled
	if embedderOnly {
		a, b := oi.comparable(), ni.comparable()
		a.EmbeddingClient, b.EmbeddingClient = config.AIConfig{}, config.AIConfig{}
		embedderOnly = hotreload.Equal(a, b)
	}
	if embedderOnly {
		e, err := buildEmbedder(ni)
		if err != nil {
			return nil, err
		}
		return hotreload.Funcs{
			CommitFunc: func() {
				m.Proxy.SwapEmbedder(e)
				logger.Info("[memory] embedding client replaced (same model)")
			},
			AbortFunc: e.Stop,
		}, nil
	}

	svc, err := m.build(next)
	if err != nil {
		return nil, err
	}
	return hotreload.Funcs{
		CommitFunc: func() {
			m.Proxy.Replace(svc)
			switch {
			case !ni.Enabled:
				logger.Info("[memory] memory subsystem disabled")
			case oi.Enabled && oi.EmbeddingModel != ni.EmbeddingModel:
				logger.Infof("[memory] memory service rebuilt for embedding model %q; its vector collection is filled from the source tier in the background", ni.EmbeddingModel)
			default:
				logger.Info("[memory] memory service rebuilt")
			}
		},
		AbortFunc: func() { _ = svc.Close() },
	}, nil
}

func buildServiceConfig(cfg *config.Config) memory.ServiceConfig {
	return memory.ServiceConfig{
		ConsolidationInterval: cfg.Memory.ConsolidationInterval,
		ShortTermLimit:        cfg.Memory.ShortTermLimit,
		TopK:                  cfg.Memory.Retrieval.TopK,
		MinScore:              cfg.Memory.Retrieval.MinScore,
		Consolidation:         &cfg.Memory.Consolidation,
		OwnBotID:              cfg.Discord.OwnBotID,
		OtherBotPolicy:        cfg.Discord.OtherBotPolicy,
		MemorySearchLimit:     cfg.Memory.Consolidation.MemorySearchLimit,
		MaxPaginatedLimit:     cfg.Memory.MaxPaginatedLimit,

		MaintenanceIntervalSec:       cfg.Memory.MaintenanceIntervalSec,
		MergeCronHourUTC:             cfg.Memory.MergeCronHourUTC,
		SummarizeCronDay:             cfg.Memory.SummarizeCronDay,
		MergeCosineThreshold:         cfg.Memory.MergeCosineThreshold,
		PruneDecayThreshold:          cfg.Memory.PruneDecayThreshold,
		PruneAgeDays:                 cfg.Memory.PruneAgeDays,
		RelationshipPruneAgeDays:     cfg.Memory.RelationshipPruneAgeDays,
		MaxMaintenanceLLMCallsPerDay: cfg.Memory.MaxMaintenanceLLMCallsPerDay,
		EntropyMinUniqueWordRatio:    cfg.Memory.EntropyMinUniqueWordRatio,
		RRFK:                         cfg.Memory.RRFK,
		ContextMaxMemories:           cfg.Memory.ContextMaxMemories,
		Scoring: memory.ScoringConfig{
			Weights: memory.ScoringWeights{
				Importance: cfg.Memory.Scoring.ImportanceWeight,
				Recency:    cfg.Memory.Scoring.RecencyWeight,
				Access:     cfg.Memory.Scoring.AccessWeight,
				Confidence: cfg.Memory.Scoring.ConfidenceWeight,
			},
			DecayRates: map[memory.Type]float64{
				memory.TypeFact:     cfg.Memory.DecayRates.Fact,
				memory.TypeEpisode:  cfg.Memory.DecayRates.Episode,
				memory.TypeInterest: cfg.Memory.DecayRates.Interest,
				memory.TypeSummary:  cfg.Memory.DecayRates.Summary,
			},
			MemoryStrengthMultiplier: cfg.Memory.MemoryStrengthMultiplier,
		},
	}
}

func buildConsolidationAIConfig(cfg *config.Config) *config.AIConfig {
	c := cfg.Memory.Consolidation
	// Endpoint and model inherit core.ai's when empty. max_tokens,
	// retry_count and timeout are required when consolidation is enabled
	// and are used as set. temperature inherits core.ai's only when the key
	// is absent; an explicit 0 is used as 0. extra_params is not inherited.
	out := &config.AIConfig{
		LLMConfig: config.LLMConfig{
			APIBaseURL:  cfg.AI.APIBaseURL,
			APIKey:      cfg.AI.APIKey,
			Model:       cfg.AI.Model,
			Timeout:     c.Timeout,
			RetryCount:  c.RetryCount,
			MaxTokens:   c.MaxTokens,
			Temperature: cfg.AI.Temperature,
			ExtraParams: c.ExtraParams,
		},
	}
	if c.APIBaseURL != "" {
		out.APIBaseURL = c.APIBaseURL
	}
	if c.APIKey != "" {
		out.APIKey = c.APIKey
	}
	if c.Model != "" {
		out.Model = c.Model
	}
	if c.Temperature != nil {
		out.Temperature = *c.Temperature
		out.ExplicitZeroTemperature = *c.Temperature == 0
	}
	return out
}

func buildEmbeddingAIConfig(cfg *config.Config) *config.AIConfig {
	embeddingCfg := cfg.AI

	if cfg.Embedding.APIBaseURL != "" {
		embeddingCfg.APIBaseURL = cfg.Embedding.APIBaseURL
	}
	if cfg.Embedding.APIKey != "" {
		embeddingCfg.APIKey = cfg.Embedding.APIKey
	}
	// retry_count and timeout are the embedding's own (required when
	// memory is on), never core.ai's.
	embeddingCfg.RetryCount = cfg.Embedding.RetryCount
	embeddingCfg.Timeout = cfg.Embedding.Timeout
	// extra_params inherits core.ai.extra_params only when the key is
	// absent (nil). An explicit value is used as is; {} sends none.
	if cfg.Embedding.ExtraParams != nil {
		embeddingCfg.ExtraParams = cfg.Embedding.ExtraParams
	}

	return &embeddingCfg
}

// buildConsolidationImageAI returns the client settings that describe
// images during consolidation: core.ai's LLM fields and image modality, with
// memory_pipeline.memory.consolidation.vision overrides applied to the
// modality. Each override key applies when it is set (an explicit 0 or {}
// included); absent keys keep image's, which in turn fall back to core.ai's
// when the client is built (ModalityModel.Resolve).
func buildConsolidationImageAI(cfg *config.Config) config.AIConfig {
	img := cfg.AI.Modalities.Image
	o := cfg.Memory.Consolidation.Vision
	img.ModalityModel = o.ModalityModel.Over(img.ModalityModel)
	if o.DescriptionPrompt != "" {
		img.Prompt = o.DescriptionPrompt
	}
	out := config.AIConfig{LLMConfig: cfg.AI.LLMConfig, MaxImageBytes: cfg.AI.MaxImageBytes}
	out.Modalities.Image = img
	return out
}
