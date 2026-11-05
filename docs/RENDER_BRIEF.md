# Page render brief

`ai/app/services/render_brief.py` assembles the versioned page render brief for
draft, ink, and painted-page generation. Stage constraints precede the same story contract.
The brief includes every `PanelTextItem` field without truncation. Array order is
the page's allocation/reading order; panel numbers remain source identifiers and
do not trigger sorting. Camera, background, actions and image-prompt details are
explicit drawing inputs.

`CharacterRenderIdentity` pairs the stored character ID, name, description and
complete visual-attribute map with its reference URL. Go sorts these identities by
character ID so repository iteration order cannot change the mapping. The shared
builder loads the images and writes their 1-based input indices, roles and SHA-256
hashes into the prompt together. Draft-conditioned painted pages attach the
approved draft first for composition, then identity references. Direct-from-brief
painted pages omit the draft and explicitly request an upright portrait 2:3 page;
the image endpoint does not accept a size parameter, so this geometry is requested
in the prompt. Character references guide identity,
not presence: a reference is not an instruction to insert that character into
every panel. All project references remain available because panels currently
have free-text actions rather than explicit cast IDs; substring matching would
silently miss aliases or characters mentioned indirectly.

Older internal callers may continue sending `character_reference_image_urls`.
Those inputs follow named references and have an unspecified-name identity role.
A URL already supplied by a named identity is not attached again as a legacy
reference. Two named identities may deliberately share a source image and retain
separate indices. No name is guessed from a URL.

Dialogue values contain spoken words only. Speaker labels such as `Nia:` are
removed from the bubble text; speakers are indicated by bubble tails and position.
The prompt forbids names or labels inside bubbles. Empty dialogue means no bubble.
Narration is retained as story context and remains
excluded from image captions by the existing image-only rules. This does not
select a deterministic lettering implementation or change approvals. Final line
art inking derives its line and tonal treatment from the shared style bible without
contradictory flat-white/no-shading exclusions. The style ID is resolved into the
versioned style bible (`style-bible-v1`) embedded directly in the render brief and
shared across reference, draft, painted-page and ink rendering, and palette suggestion.

Default character-reference generation now includes name, description and the
stored visual attributes (including age, appearance, hair and clothing when
present). An explicitly supplied custom prompt retains its existing precedence.

Tests include a checked-in golden brief, per-field edits, unrelated-story HTTP
regressions, late/reallocated panels, exact Unicode dialogue, reference bytes and
indices through a mocked Meta HTTP transport, and Go handler identity assembly.
These prove request fidelity, not generated artistic quality. No extra image call
or provider control is introduced; longer instructions may affect moderated text
usage. Reference hashes in the prompt identify inputs and are not persisted output
metadata or provenance verification.
