// Package vision provides image description using vision-capable models for hybrid vision mode.
package vision

import (
	"context"
	"fmt"
	"strings"

	"ezyapper/internal/ai"
	"ezyapper/internal/config"

	openai "github.com/sashabaranov/go-openai"
)

// VisionDescriber handles image description using vision-capable models.
// It supports both URL-based and base64-encoded image inputs.
type VisionDescriber struct {
	client            *ai.Client
	visionModel       string
	descriptionPrompt string
	maxTokens         int
	temperature       float32
	visionBase64      bool
	extraParams       map[string]any
}

// NewVisionDescriber creates a new vision describer with the given client and vision configuration.
func NewVisionDescriber(client *ai.Client, visionConfig *config.VisionConfig, aiConfig *config.AIConfig) *VisionDescriber {
	maxTokens := visionConfig.MaxTokens
	temperature := visionConfig.Temperature

	return &VisionDescriber{
		client:            client,
		visionModel:       aiConfig.VisionModel,
		descriptionPrompt: visionConfig.DescriptionPrompt,
		maxTokens:         maxTokens,
		temperature:       temperature,
		visionBase64:      aiConfig.VisionBase64,
		extraParams:       visionConfig.ExtraParams,
	}
}

// DescribeImages generates text descriptions for all provided image URLs using a vision model.
func (v *VisionDescriber) DescribeImages(ctx context.Context, imageURLs []string) ([]string, error) {
	if len(imageURLs) == 0 {
		return nil, fmt.Errorf("no images provided")
	}

	descriptions := make([]string, 0, len(imageURLs))

	for _, url := range imageURLs {
		description, err := v.DescribeSingleImage(ctx, url)
		if err != nil {
			return nil, fmt.Errorf("failed to describe image: %w", err)
		}
		descriptions = append(descriptions, description)
	}

	return descriptions, nil
}

// DescribeSingleImage generates a text description for a single image URL.
func (v *VisionDescriber) DescribeSingleImage(ctx context.Context, imageURL string) (string, error) {
	imagePart := openai.ChatMessagePart{}

	if strings.HasPrefix(imageURL, "data:image") {
		imagePart = openai.ChatMessagePart{
			Type: openai.ChatMessagePartTypeImageURL,
			ImageURL: &openai.ChatMessageImageURL{
				URL:    imageURL,
				Detail: openai.ImageURLDetailAuto,
			},
		}
	} else if v.visionBase64 {
		base64Data, err := v.client.DownloadImage(ctx, imageURL)
		if err != nil {
			return "", fmt.Errorf("failed to convert image to base64: %w", err)
		}
		imagePart = openai.ChatMessagePart{
			Type: openai.ChatMessagePartTypeImageURL,
			ImageURL: &openai.ChatMessageImageURL{
				URL:    base64Data,
				Detail: openai.ImageURLDetailAuto,
			},
		}
	} else {
		imagePart = openai.ChatMessagePart{
			Type: openai.ChatMessagePartTypeImageURL,
			ImageURL: &openai.ChatMessageImageURL{
				URL:    imageURL,
				Detail: openai.ImageURLDetailAuto,
			},
		}
	}

	messages := []openai.ChatCompletionMessage{
		{
			Role: openai.ChatMessageRoleUser,
			MultiContent: []openai.ChatMessagePart{
				{
					Type: openai.ChatMessagePartTypeText,
					Text: v.descriptionPrompt,
				},
				imagePart,
			},
		},
	}

	req := openai.ChatCompletionRequest{
		Model:       v.visionModel,
		Messages:    messages,
		MaxTokens:   v.maxTokens,
		Temperature: v.temperature,
	}

	// Apply extra parameters from config
	ai.ApplyExtraParams(&req, v.extraParams, "[vision]")

	resp, err := v.client.CreateChatCompletionWithRetry(ctx, req, "vision describer completion")
	if err != nil {
		return "", fmt.Errorf("vision completion failed: %w", err)
	}

	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("no response choices returned")
	}

	return resp.Choices[0].Message.Content, nil
}
