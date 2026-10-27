# Desktop design

A quiet reading desk for a local PDF library. The primary action is searching
for an idea; documents and page numbers carry more visual weight than scores.

Palette: paper #ffffff, fog #edf2f6, ink #203248, slate #607186,
annotation blue #285fc7, success green #2d7659.
Typography: Segoe UI / Noto Sans / system sans for controls and result names;
Georgia for the selected page's reading text. No remote fonts or assets.

Layout, left aligned:

    Library sidebar       Subject search
    Saved libraries       Query input and Search button
    Index state           Ranked pages       Selected page text
    Update / Cancel       Filename / page    Open PDF
    Local model status

Use one clear search field, not a dashboard of cards. Folder and index state
stay in a narrow sidebar; the main area becomes a two-pane document browser
after a search. Scores are secondary hints, not percentages or probabilities.
A page-shaped empty state communicates the task without fake results.

The design intentionally avoids a marketing hero, gradients, repeated cards,
and decorative statistics. Standard title-bar/window controls remain native.
Every interaction has keyboard focus and a text label. Motion is limited to
busy feedback and is disabled under reduced-motion preferences.

Saved libraries use a compact, scrollable list in the existing sidebar. Names
and paths are left aligned; the current library uses annotation blue and the
existing selected-row background. Rename forms and removal confirmation appear
inline, with explicit text actions. The established palette, typography and
search layout stay consistent as the list grows.
