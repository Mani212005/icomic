package api

import (
	"encoding/json"
	"sort"

	"icomic/backend/gen"
	"icomic/backend/internal/domain"
)

// characterRenderIdentities gives both page stages the same stable name/image mapping.
// Keep all project references: panels currently have free text, not character IDs,
// so guessing presence from name substrings could silently lose identity anchors.
func characterRenderIdentities(characters []*domain.Character) *[]gen.CharacterRenderIdentity {
	identities := make([]gen.CharacterRenderIdentity, 0, len(characters))
	for _, ch := range characters {
		if ch.ReferenceImageURL == "" {
			continue
		}
		attributes := ch.VisualAttributes
		if attributes == nil {
			attributes = map[string]interface{}{}
		}
		identities = append(identities, gen.CharacterRenderIdentity{
			CharacterID: ch.ID, Name: ch.Name, Description: ch.Description,
			VisualAttributes: attributes, ReferenceImageURL: ch.ReferenceImageURL,
		})
	}
	sort.Slice(identities, func(i, j int) bool {
		return identities[i].CharacterID < identities[j].CharacterID
	})
	return &identities
}

func defaultCharacterReferencePrompt(ch *domain.Character) (string, error) {
	identity := struct {
		Name             string                 `json:"name"`
		Description      string                 `json:"description"`
		VisualAttributes map[string]interface{} `json:"visual_attributes"`
	}{ch.Name, ch.Description, ch.VisualAttributes}
	data, err := json.Marshal(identity)
	return string(data), err
}
