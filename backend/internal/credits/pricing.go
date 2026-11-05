package credits

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"strconv"
	"strings"

	"icomic/backend/internal/domain"
)

const (
	defaultImageModelID   = "muse-image-1.0"
	defaultVisionModelID  = "muse-spark-1.3"
	defaultPricingVersion = "default"
)

// PricingConfig is the backend's single source for estimates, generation debits,
// model labels, and pricing-version metadata. Default amounts preserve the
// documented fake/development basis; estimates remain explicitly unverified
// until the operator confirms the configured price source.
type PricingConfig struct {
	ImageModelID      string
	VisionModelID     string
	ImagePriceUSD     float64
	ColourEstimateUSD float64
	Version           string
	Verified          bool
}

func DefaultPricing() PricingConfig {
	return PricingConfig{
		ImageModelID:      defaultImageModelID,
		VisionModelID:     defaultVisionModelID,
		ImagePriceUSD:     ImageUnitPrice,
		ColourEstimateUSD: ColourSuggestionUnitPrice,
		Version:           defaultPricingVersion,
	}
}

// PricingFromEnv reads optional operator configuration without contacting a
// provider. Invalid values fail startup rather than silently changing quotes.
func PricingFromEnv(getenv func(string) string) (PricingConfig, error) {
	config := DefaultPricing()
	customized := false
	explicitVersion := strings.TrimSpace(getenv("PRICING_VERSION"))
	for key, target := range map[string]*string{
		"META_IMAGE_MODEL":  &config.ImageModelID,
		"META_VISION_MODEL": &config.VisionModelID,
	} {
		if value := strings.TrimSpace(getenv(key)); value != "" {
			customized = customized || value != *target
			*target = value
		}
	}
	for key, target := range map[string]*float64{
		"META_IMAGE_PRICE_USD":     &config.ImagePriceUSD,
		"META_COLOUR_ESTIMATE_USD": &config.ColourEstimateUSD,
	} {
		if value := strings.TrimSpace(getenv(key)); value != "" {
			price, err := strconv.ParseFloat(value, 64)
			if err != nil || math.IsNaN(price) || math.IsInf(price, 0) || price <= 0 {
				return PricingConfig{}, fmt.Errorf("%s must be a finite positive USD amount", key)
			}
			customized = customized || price != *target
			*target = price
		}
	}
	verified := strings.TrimSpace(getenv("PRICING_VERIFIED"))
	if verified != "" && !strings.EqualFold(verified, "true") && !strings.EqualFold(verified, "false") {
		return PricingConfig{}, fmt.Errorf("PRICING_VERIFIED must be true or false")
	}
	config.Verified = strings.EqualFold(verified, "true")
	customized = customized || config.Verified
	if config.ImageModelID == "" || config.VisionModelID == "" || config.Version == "" {
		return PricingConfig{}, fmt.Errorf("pricing model IDs and version must not be empty")
	}
	if explicitVersion != "" {
		config.Version = explicitVersion
	} else if customized {
		fingerprint := strings.Join([]string{
			config.ImageModelID,
			config.VisionModelID,
			strconv.FormatFloat(config.ImagePriceUSD, 'g', -1, 64),
			strconv.FormatFloat(config.ColourEstimateUSD, 'g', -1, 64),
			strconv.FormatBool(config.Verified),
		}, "|")
		digest := sha256.Sum256([]byte(fingerprint))
		config.Version = "pricing-" + hex.EncodeToString(digest[:6])
	}
	return config, nil
}

func (p PricingConfig) Estimate(stage string, count int) domain.CostEstimate {
	if count < 1 {
		count = 1
	}

	stageName := stage
	modelID := p.ImageModelID
	unitPrice := p.ImagePriceUSD
	imageCount := count
	assumptions := []string{"Configured image attempt price; provider cost and retail pricing are not independently verified."}
	isEstimate := false
	switch stage {
	case "draft", "draft_sketch":
		stageName = "draft_sketch"
	case "line_art", "final_line_art":
		stageName = "final_line_art"
	case "character_reference", "reference":
		stageName = "character_reference"
	case "colour_suggestion", "colour":
		stageName = "colour_suggestion"
		unitPrice = p.ColourEstimateUSD
		imageCount = 0
		modelID = p.VisionModelID
		assumptions = []string{"Configured palette estimate; actual token usage and price are not verified."}
		isEstimate = true
	default:
		stageName = stage
		assumptions = []string{"No configured pricing basis for this stage; value is an unverified image-price fallback."}
		isEstimate = true
	}
	if modelID == "" {
		modelID = p.ImageModelID
	}
	return domain.CostEstimate{
		StageName:       stageName,
		ImageCount:      imageCount,
		EstimatedCost:   float64(count) * unitPrice,
		Assumptions:     assumptions,
		IsEstimatePrice: isEstimate,
		ModelID:         modelID,
		Currency:        "USD",
		PricingVersion:  p.Version,
		PriceVerified:   p.Verified,
	}
}
