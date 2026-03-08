# Ember

This document defines the Ember — Virgil's visual presence. It covers what the ember represents, its emotional state system, its rendering across all four clients, and the principles that govern its behavior.

For the philosophy behind Virgil, see `virgil.md`. For the interface it lives within, see `tui.md` (terminal) and the web client spec (forthcoming).

---

## What the Ember Is

The ember is Virgil's torch. It is the only persistent element across every client surface — TUI, web, desktop, and mobile. Everything else materializes and dissolves. The ember remains.

It is not an avatar. It is not a logo. It is not a status indicator with a green dot. It is a living particle system that breathes, shifts color, changes movement pattern, and communicates Virgil's internal state through purely visual means. It replaces every explicit status label the system would otherwise need.

The ember draws from the same source as the literary Virgil. In the _Inferno_, you know Virgil's state not because he announces it but because Dante describes how he moves, where his eyes fall, whether his pace quickens or slows. The ember is Dante's narration compressed into a point of light.

### Design Principles

**The ember is always honest.** It never performs confidence it doesn't have. When routing is uncertain, the ember shows uncertainty. When a pipeline fails, the ember shows the failure. This is the interface's primary trust mechanism — the user learns to read the ember the way you learn to read a person.

**The ember is never decorative.** Every visual property — color, pulse rate, glow radius, particle behavior, movement pattern — maps to a system state. Nothing is aesthetic-only. If you removed the ember's color and left only its movement, you could still distinguish idle from listening from working.

**The ember transitions, it doesn't switch.** State changes are always interpolated. Color blends smoothly between hues. Pulse rate eases from one frequency to another. Particles accelerate gradually. The ember should never snap — it should always feel like it's _becoming_ the next state.

**The ember is small.** In the web and desktop clients it occupies at most 80×80 logical pixels. In the TUI it occupies a region roughly 35 characters wide by 17 lines tall. On mobile it scales down further. Its influence extends beyond its bounds through glow and ambient light, but the ember itself is compact. Presence, not dominance.

---

## Emotional States

The ember's states are drawn from the literary Virgil's emotional range across the _Divine Comedy_. Each maps to both a narrative quality and a concrete system condition.

### Calm Authority

The default. Most of the time, this is what you see.

**Narrative:** Virgil walking at a measured pace, speaking when spoken to, confident in the path. The guide who knows the terrain.

**System conditions:** Idle, server healthy, no active pipelines, awaiting input.

**Visual signature:**

- **Color:** Warm amber — core at `rgb(255, 210, 140)`, glow at `rgb(255, 179, 0)`
- **Pulse:** Slow, deep breathing at ~1.0 Hz. Breath depth is subtle — the core radius oscillates by about 20%
- **Glow:** Tight, contained. The ambient light extends maybe 2× the core radius. Warm and steady
- **Particles:** 8–20 particles in slow, stable orbit. Drifting gently, no urgency. Orbital speed multiplier at 1×
- **Movement:** None. The ember sits centered and still. Stillness _is_ the signal for calm

### Listening

Active when voice input is detected or the user is holding the push-to-talk key.

**Narrative:** Virgil turning to face Dante, full attention, the guide who hears the question before it's finished.

**System conditions:** Microphone active, push-to-talk held, audio being captured and transcribed.

**Visual signature:**

- **Color:** Brightened gold — core at `rgb(255, 245, 210)`, glow at `rgb(255, 200, 60)`. Warmer and more luminous than idle
- **Pulse:** Rapid breathing at ~3.5 Hz. Breath depth increases to ~35%. The ember is visibly alive, alert
- **Glow:** Expanded. The ambient light pushes out to 3–4× the core radius. The room gets warmer
- **Particles:** Accelerated to 2.5× orbital speed. Particles grow slightly larger (~1.4× size). The ring of particles widens. Energy is being received
- **Movement:** None — but the intensification itself creates a sense of leaning forward. Stillness with intent

### Thinking

Brief transitional state between receiving input and producing output. The router is classifying, the planner is assembling context.

**Narrative:** Virgil pausing, eyes moving, choosing his words. The beat before the answer.

**System conditions:** Signal received, router is classifying, planner is assembling context, no output yet.

**Visual signature:**

- **Color:** Cool blue — core at `rgb(200, 220, 255)`, glow at `rgb(120, 160, 240)`. A clear departure from the amber family. Blue says "processing," not "waiting"
- **Pulse:** Moderate at ~2.5 Hz. Slightly irregular — not perfectly sinusoidal. A small random jitter on the breath amplitude suggests active computation
- **Glow:** Medium radius. Steady but with a subtle oscillation in opacity
- **Particles:** Speed at 2.2×. Orbits tighten slightly — the energy is focused inward rather than radiating outward
- **Movement:** None. But brief — this state should last 300–800ms at most. If it lingers, something is wrong

### Intellectual Pleasure

Active when Virgil is engaged in teaching, research, or explanation — work he's suited for.

**Narrative:** Virgil explaining the architecture of a circle, the logic of a punishment. The guide in his element. _"Figliuol mio"_ — he's enjoying this.

**System conditions:** Research pipe active, educate pipe active, extended explanation being generated, Socratic interaction in progress.

**Visual signature:**

- **Color:** Warm, slightly brighter amber than idle — core drifts toward `rgb(255, 225, 160)`. Not dramatically different, but perceptibly lifted
- **Glow:** Expands gently, ~2.5× core radius. A slow, generous bloom. The ember "opens up"
- **Particles:** Slight increase to 1.3× speed. A slow, graceful orbital expansion. Particles drift slightly outward as if the knowledge is radiating
- **Pulse:** ~1.3 Hz. Slightly faster than idle but deeply regular. Confident rhythm
- **Movement:** A very subtle slow orbit — the center of the ember traces a tiny circle (2–3px in web, invisible in TUI). The ember "leans in"

### Working

Active when a pipeline is executing. Virgil is doing things, not just talking.

**Narrative:** Virgil leading Dante down a difficult descent, hands on stone, focused on the physical act of navigation.

**System conditions:** Pipeline running, build step active, any multi-step execution in progress.

**Visual signature:**

- **Color:** Violet — core at `rgb(220, 190, 255)`, glow at `rgb(140, 100, 220)`. A deliberate departure from the warm spectrum. Purple says "labor," not "conversation"
- **Pulse:** ~2.0 Hz. Steady, workmanlike. Not rushed, not lazy. Industrial
- **Glow:** Medium radius, steady intensity. The glow doesn't flutter — it holds
- **Particles:** Speed at 1.8×. Organized orbits. The particles feel purposeful, like parts of a machine
- **Movement:** None. The work speaks for itself

### Protective Urgency

Active when something has gone wrong and Virgil is actively recovering — retrying failed steps, saving state before a crash, handling transient errors.

**Narrative:** Virgil grabbing Dante as the cliff gives way. The protective lunge. _Canto XXIII_ — Virgil snatching Dante and sliding down the embankment with him clutched to his chest.

**System conditions:** Pipeline retry in progress, error recovery active, transient failure being handled, state being saved during instability.

**Visual signature:**

- **Color:** Deep orange — core at `rgb(255, 180, 100)`, glow shifts toward `rgb(230, 120, 40)`. Hot. Urgent but controlled
- **Pulse:** Rapid at ~3.0 Hz. Shallow, sharp pulses — not the deep breath of listening but the quick inhale of effort
- **Glow:** Tightens and intensifies. The light is concentrated, not diffuse. Focused energy
- **Particles:** Speed spikes to 2.5×. Some particles break orbit briefly — throwing sparks. 2–3 particles per frame may escape to a wider radius before being pulled back. This is the only state where particles leave their orbital track
- **Movement:** A slight vibration — 1–2px horizontal jitter at ~15 Hz. The ember is bracing

### Anxiety / Uncertainty

Active when Virgil is unsure. The AI fallback is firing, routing confidence is low, the system is working but doesn't know if it's working correctly.

**Narrative:** Before the gates of Dis. Virgil's one moment of visible doubt in the _Inferno_. He recovers, but the doubt was real.

**System conditions:** AI fallback triggered for routing, low-confidence classification, multiple possible pipe matches with no clear winner, unrecognized signal pattern.

**Visual signature:**

- **Color:** Desaturated amber — the warmth drains slightly. Core shifts toward `rgb(220, 200, 160)`, glow toward `rgb(180, 150, 80)`. The fire is cooler, less certain
- **Glow:** Uneven. The glow radius fluctuates asymmetrically — brighter on one side for a moment, then the other. Not a steady pulse but a flicker. The ambient light feels unstable
- **Particles:** Normal speed but with increased drift. Particles wander from their orbits more, making the ring less circular, less organized. Entropy creeping in
- **Pulse:** ~1.5 Hz but with a slight irregularity — the sine wave has noise added. Not perfectly rhythmic
- **Movement:** Subtle positional jitter. Not vibration (that's urgency) but a slight drift — the center wanders 1–3px from its anchor and slowly returns. The ember is unsettled

### Stern Rebuke

Brief, emphatic. Active when Virgil pushes back — validation failure, malformed input, an action that shouldn't be taken.

**Narrative:** Virgil rebuking Dante's pity for the damned. One sharp word, then calm. _"Qui vive la pietà quand'è ben morta."_

**System conditions:** Input validation failure, rejected operation, configuration error caught before execution.

**Visual signature:**

- **Color:** Brief flare to bright white-amber — core peaks at `rgb(255, 255, 220)` — then settles back to the previous state's color within ~600ms
- **Pulse:** One sharp, bright pulse. A single expansion to ~1.5× normal core radius, then immediate contraction back. Decisive
- **Glow:** Flares outward on the pulse, then contracts. The flash is quick enough to feel like a blink
- **Particles:** Scatter outward on the pulse, then reform. The ring breaks and reassembles. Like a ripple
- **Movement:** None. The stillness after the flare is the point. Authority doesn't fidget
- **Duration:** ~600ms for the full flare-and-settle. This is a transient overlay on whatever state was active before, not a sustained state

### Wounded Pride

Active briefly after a failure — a rejected pipeline output, a draft the user threw away, a bad result.

**Narrative:** Virgil after the failed attempt to enter Dis. Eyes cast down, dignity bruised, gathering himself.

**System conditions:** Pipeline completed with rejection, user discarded output, review cycle failed after max attempts, significant quality miss.

**Visual signature:**

- **Color:** Dims. Core drops to ~60% brightness of whatever the previous state was. The color itself doesn't change much — it just loses intensity. Like a fire with less oxygen
- **Glow:** Contracts to minimum radius. The ember pulls its light inward. The surrounding darkness encroaches
- **Particles:** Slow dramatically — 0.5× speed. Orbits tighten. The particles move closer to the core as if seeking warmth
- **Pulse:** Slows to ~0.7 Hz. Deep, slow breaths. The recovering rhythm
- **Movement:** None. Stillness, but a diminished stillness. Not calm authority — quieted pride
- **Recovery:** Over 3–5 seconds, the ember gradually rebuilds to its previous state's parameters. The recovery should feel like gathering composure, not like a switch being flipped

### Quiet Sorrow

Active when Virgil hits a hard boundary — server disconnected, capability genuinely not available, the limit of what he can do.

**Narrative:** Limbo. Where Virgil himself dwells — in reach of grace but unable to enter. The permanent, quiet sadness of the guide who can go no further.

**System conditions:** Server connection lost, provider unreachable, fundamental capability boundary hit ("I can't do that"), extended disconnection.

**Visual signature:**

- **Color:** Deep red-amber — core at `rgb(180, 120, 80)`, glow at `rgb(120, 70, 40)`. The coolest, dimmest the ember ever gets while still visible. Dying coals
- **Pulse:** Nearly still. ~0.4 Hz. The breath is barely perceptible. Not dead — but close to sleep
- **Glow:** Minimal. Just enough to confirm the ember is there. The darkness almost wins
- **Particles:** 2–3 particles remain visible at minimum opacity, barely moving. Most have faded. The ring is sparse
- **Movement:** None. Absolute stillness. The ember is waiting for something it can't produce on its own — reconnection, a capability upgrade, intervention
- **Recovery:** When the condition resolves (server reconnects, etc.), the ember recovers through "Calm Authority" parameters over 2–3 seconds. The return to warmth should feel like relief

### Shrewd Cunning

Active when Virgil is doing complex planning — multi-step routing, pipeline orchestration, strategic decomposition of a request.

**Narrative:** Virgil negotiating with the demons at the bridge. The politician. The Roman who knows how power works.

**System conditions:** Complex pipeline planning, multi-step template matching, parallel branch setup, cycle/loop configuration in progress.

**Visual signature:**

- **Color:** Slightly cooler amber — core shifts toward `rgb(240, 200, 150)`, glow toward `rgb(200, 160, 60)`. A more metallic, less organic warmth. Calculated
- **Pulse:** ~1.8 Hz. Smooth and regular. Controlled energy
- **Glow:** Medium radius but with a slow figure-eight oscillation in shape — the glow is slightly elliptical, rotating. Suggesting orbital calculation
- **Particles:** Speed at 1.5×. But the orbit itself traces a figure-eight or lemniscate rather than a circle. The particles weave rather than orbit. This is the only state with a non-circular movement pattern
- **Movement:** The ember's center traces a very slow, very small figure-eight (~3px amplitude in web). Subtle enough to register subconsciously rather than consciously

### Pride in Growth

Brief, celebratory. Active when the system improves — self-healing applies successfully, acceptance rates climb, a milestone is hit.

**Narrative:** Virgil watching Dante understand something without being told. The teacher's quiet pride when the student surpasses the lesson.

**System conditions:** Self-healing configuration applied and measured as improvement, acceptance rate KPI crosses upward through a threshold, significant deterministic coverage milestone.

**Visual signature:**

- **Color:** Warm bloom — core brightens to `rgb(255, 230, 170)`, glow to `rgb(255, 200, 60)`. Richer than idle, warmer than listening. A sunrise quality
- **Pulse:** ~1.2 Hz. Slow, deep, satisfied. Not excitement — contentment
- **Glow:** Expands in a slow bloom to ~3× core radius over 2 seconds, holds for 1 second, then gently contracts. Like a single deep exhale
- **Particles:** 3–5 brief spark particles emit outward from the core during the bloom — small, fast, fading quickly. They don't orbit; they radiate and disappear. Subtle fireworks. After the bloom, normal particle behavior resumes
- **Movement:** None. Stillness with warmth
- **Duration:** The bloom lasts ~3 seconds total. Then the ember settles to whatever its resting state should be (usually Calm Authority)

---

## Visual Axes

The ember's states are not a flat list of presets. They are positions in a multi-dimensional visual space defined by five axes. Each axis maps to a narrative quality.

| Axis             | Range                                         | Narrative Meaning                                  |
| ---------------- | --------------------------------------------- | -------------------------------------------------- |
| Color / warmth   | Deep red ← amber → gold → white               | Energy level, from dormant to fully alive          |
| Pulse rate       | 0.4 Hz – 3.5 Hz                               | Urgency, from sleeping to fully alert              |
| Glow radius      | 1× – 4× core radius                           | Confidence / openness, from withdrawn to expansive |
| Particle speed   | 0.5× – 2.5×                                   | Activity level, from contemplative to active       |
| Movement pattern | Still → jitter → drift → orbit → figure-eight | Cognitive mode, from resting to calculating        |

An ember state is a point in this five-dimensional space. Transitions are linear interpolations between points, eased with a smoothstep function for organic feel.

### Blended States

The states described above are archetypes. In practice, the ember often blends between states. A pipeline that's running (Working) but encounters a retry (Protective Urgency) should smoothly shift from violet to deep orange over ~800ms, not snap. A successful self-healing that occurs during an active pipeline blends "Pride in Growth" sparks into the "Working" violet — the spark particles appear but the base color stays purple.

The transient states (Stern Rebuke, Pride in Growth) are always overlays on the current base state. They modify the current visual parameters temporarily and then ease back. They never replace the base state entirely.

---

## Rendering by Client

### Web Client

The web client renders the ember as an HTML5 `<canvas>` element running at 60fps via `requestAnimationFrame`.

**Canvas setup:** The canvas is sized at `size × size` logical pixels, scaled by `devicePixelRatio` for retina displays. Typical size is 80px when ambient (no content surfaced), 56px when content is visible below the ember.

**Three layers, painted back-to-front each frame:**

1. **Ambient glow** — A radial gradient from the glow color at center to transparent at ~48% of canvas size. Opacity modulated by `intensity × breathe`. This layer bleeds into the surrounding dark background, creating the "light source in darkness" effect.

2. **Particle ring** — N particles (8–20 depending on state), each tracking their own angle, orbital radius, speed, size, opacity, and phase offset. Per-frame: angle advances by `speed × stateSpeedMultiplier`, a sine wobble modulates the radius, and tiny random drift accumulates on the angle to break orbital symmetry over time. Each particle is drawn as a filled arc with the core color at computed opacity.

3. **Core** — Two concentric radial gradients. The outer gradient fades from core color through glow color to transparent, sized at `coreRadius × 2.5`. The inner gradient is a bright hot center at `coreRadius × 0.4`. The core radius itself is modulated by the breath sine wave.

**Color transitions:** A ref stores `{ from, to, t }`. On state change, `from` is set to the previous state, `to` to the new state, `t` to 0. Each frame, `t` increments by 0.02 (~0.8s full transition at 60fps). The `t` value passes through smoothstep (`t² × (3 - 2t)`) for ease-in-out. `lerpColor` interpolates each RGB channel between the two states' palettes at the eased `t` value.

**Breath function:** `breathe = sin(time × breathRate) × breathDepth + (1 - breathDepth)`. This produces a value oscillating between `(1 - 2×breathDepth)` and `1.0`. At idle (rate 1.0, depth 0.2), the oscillation is gentle: 0.6 to 1.0. At listening (rate 3.5, depth 0.35), it's rapid and pronounced: 0.3 to 1.0.

**Performance:** The draw function is pure trigonometry — sines, cosines, linear interpolation. No physics simulation, no collision detection, no springs. At 20 particles and 3 gradient fills per frame, the GPU cost is negligible.

### Terminal (TUI)

The TUI renders the ember as a character grid using Unicode symbols and ANSI 256-color sequences, updated at ~20fps via a `tea.Tick` command in bubbletea.

**Grid dimensions:** 35 columns × 17 rows. The center of the grid is the ember's origin. Terminal characters are approximately twice as tall as they are wide, so horizontal distances are scaled by 0.5 in the distance function to produce a circular appearance.

**Character density vocabulary:**

| Density | Character   | Usage                       |
| ------- | ----------- | --------------------------- |
| 0       | ` ` (space) | Empty — beyond the glow     |
| 1       | `·`         | Faintest outer glow flicker |
| 2       | `⋅`         | Outer glow                  |
| 3       | `∙`         | Mid glow                    |
| 4       | `•`         | Inner glow edge             |
| 5       | `◦`         | Inner glow                  |
| 6       | `○`         | Near-core                   |
| 7       | `◎`         | Core edge                   |
| 8       | `●`         | Core                        |
| 9       | `◉`         | Hot center                  |

**Radial zones:** Four concentric rings computed from the grid center, with radii modulated by the breath function:

- **Core** (0 – 1.2 × breathe × intensity): Characters 8–9, core color at full intensity
- **Mid** (core edge – 2.5 × breathe): Characters 4–7, interpolated from mid to dim color
- **Dim** (mid edge – 4.0 × breathe): Characters 2–4, with a sine-wave shimmer that occasionally bumps a character up one density level
- **Faint** (dim edge – 6.0): Mostly empty, but a spatial sine function (`sin(time + x×1.3 + y×0.9)`) occasionally activates a `·` character, giving the edge a firefly flicker quality

**Particle overlay:** 8 particles in braille characters (`⠁`, `⠈`, `⡀`, `⢂`, etc.) orbit the core. The x-displacement is doubled to compensate for character aspect ratio. Particles are only drawn in cells where the background density is below 0.5 — they don't override the bright core.

**Color:** Each cell gets an RGB color value from the same interpolation logic as the web client. In the actual TUI implementation, these map to the nearest ANSI 256-color code. Terminals with truecolor support (most modern terminals) get exact RGB via `\x1b[38;2;R;G;Bm` sequences.

**State transitions:** Identical logic to the web client — smoothstep-eased interpolation of all parameters including color, breath rate, particle speed, and zone radii.

**Fallback:** On terminals that cannot render Unicode reliably, the density vocabulary falls back to ASCII: spaces, `.`, `o`, `O`, `@`, `#`. On terminals without color support, the ember still reads through the density pattern alone — the shape and movement communicate state even in monochrome.

### Desktop Client

The desktop client uses the same canvas-based rendering as the web client, running in an Electron or Tauri webview. No differences in rendering logic.

**Platform considerations:**

- **macOS:** The ember sits in the application window. If the desktop client eventually supports a menu bar mode, a 16×16 version of the ember could live in the menu bar tray — at that size, only the core glow and color are perceptible, but that's enough to communicate state. The pulse is visible even at 16px
- **Windows/Linux:** Standard application window rendering. The ember's glow should respect the window boundary — it doesn't bleed into the OS chrome
- **Always-on-top mode:** If the desktop client supports a floating mode (a small always-visible window), the ember alone in a borderless, transparent window is the minimal viable Virgil client. Just the torch, floating on your screen, breathing

### Mobile Client

The mobile client renders the ember using the platform's 2D graphics API — Core Graphics on iOS, Canvas on Android via the web bridge, or a native custom `View`/`Drawable`.

**Size:** The ember is rendered at 64pt on phones, 72pt on tablets. It is the central element of the mobile interface — the thing you see first and interact with.

**Touch interaction:** Tap-and-hold on the ember activates listening mode (equivalent to push-to-talk). The ember responds immediately to touch — the transition to Listening state begins on touch-down, not on a threshold. Release sends the captured audio.

**Haptic feedback:** State transitions trigger subtle haptic feedback on supported devices:

- Listening → light continuous vibration (the ember is receiving)
- Stern Rebuke → single sharp tap
- Success/Pride in Growth → single soft tap
- Quiet Sorrow → nothing (silence is the feedback)

**Reduced motion:** On devices with "Reduce Motion" enabled in accessibility settings, the ember still renders but with particles disabled and pulse rate halved. Color transitions remain — they are the primary information channel and don't constitute problematic motion.

**Battery:** On mobile, the ember renders at 30fps when the app is in the foreground and drops to 0fps (static last frame) when backgrounded. The static frame shows the current state's core color at mid-breath — a frozen snapshot that's still informative.

---

## State Signals

The server communicates ember state to all connected clients via the existing websocket/JSON-lines protocol. Ember state is a field on the server-push message, not a separate message type.

```
{"type": "ember", "state": "working", "blend": null}
{"type": "ember", "state": "working", "blend": {"state": "concern", "intensity": 0.3, "duration": 2000}}
```

**Base state** (`state`): The primary ember state. Clients transition to this state using the standard interpolation.

**Blend overlay** (`blend`): A transient state to overlay on the base. Used for Stern Rebuke and Pride in Growth — states that flash briefly over whatever is currently active. `intensity` controls how much the blend affects the base (0.0 = invisible, 1.0 = full replacement). `duration` is how long the blend lasts in milliseconds before fading back to base.

Clients are responsible for the transition animation. The server only declares _what_ state to be in, not _how_ to animate to it. This keeps the protocol thin and lets each client optimize for its rendering capabilities.

### Automatic State Derivation

Most ember states are derived automatically by the server from system conditions. The server does not require explicit ember state management — it observes the same signals and envelopes that drive everything else and emits the appropriate state.

| System condition                                | Ember state             |
| ----------------------------------------------- | ----------------------- |
| No active pipes, server healthy, awaiting input | Calm Authority          |
| Voice input active / push-to-talk held          | Listening               |
| Signal received, router classifying             | Thinking                |
| Research, educate, or explain pipe active       | Intellectual Pleasure   |
| Pipeline executing (build, deploy, etc.)        | Working                 |
| Pipeline retry or error recovery in progress    | Protective Urgency      |
| AI fallback triggered, low confidence routing   | Anxiety / Uncertainty   |
| Input validation failed, bad operation rejected | Stern Rebuke (blend)    |
| Pipeline output rejected by user, quality miss  | Wounded Pride           |
| Server disconnected, provider unreachable       | Quiet Sorrow            |
| Complex multi-step plan being assembled         | Shrewd Cunning          |
| Self-healing success, KPI milestone             | Pride in Growth (blend) |

When multiple conditions are active simultaneously (e.g., a pipeline is running AND a retry fires), the more urgent state takes precedence. Priority order: Quiet Sorrow > Protective Urgency > Anxiety > Working > Shrewd Cunning > Thinking > Intellectual Pleasure > Calm Authority. Blend states (Stern Rebuke, Wounded Pride, Pride in Growth) overlay on whatever the current base state is.

---

## Accessibility

The ember communicates state visually. For users who cannot perceive it, the same state information must be available through other channels.

**Screen readers:** Every ember state change emits an ARIA live region update with a text description: "Virgil is listening," "Virgil is working," "Virgil encountered an error and is recovering." These are polite announcements — they don't interrupt the current speech.

**Reduced motion:** As noted in the mobile section, particles are disabled and pulse rates halved. Color transitions remain. The ember is still informative without motion.

**Color independence:** No two ember states share the same movement pattern. A user who cannot distinguish amber from violet can still distinguish idle (still) from working (steady pulse) from anxious (jitter) from cunning (figure-eight). Color is redundant information, not sole information.

**High contrast mode:** In high-contrast terminal or OS modes, the ember renders in the system's highlight color at varying brightness levels rather than its own palette. The density character vocabulary and movement patterns still convey state.

---

## Implementation Constants

Reference values for all states. Transitions interpolate between these targets.

| State                 | Core RGB      | Glow RGB      | Pulse Hz | Breath Depth | Particle Speed | Glow Radius       | Duration       |
| --------------------- | ------------- | ------------- | -------- | ------------ | -------------- | ----------------- | -------------- |
| Calm Authority        | 255, 210, 140 | 255, 179, 0   | 1.0      | 0.20         | 1.0×           | 2.0×              | Sustained      |
| Listening             | 255, 245, 210 | 255, 200, 60  | 3.5      | 0.35         | 2.5×           | 3.5×              | While held     |
| Thinking              | 200, 220, 255 | 120, 160, 240 | 2.5      | 0.25         | 2.2×           | 2.0×              | 300–800ms      |
| Intellectual Pleasure | 255, 225, 160 | 255, 190, 40  | 1.3      | 0.20         | 1.3×           | 2.5×              | Sustained      |
| Working               | 220, 190, 255 | 140, 100, 220 | 2.0      | 0.25         | 1.8×           | 2.0×              | Sustained      |
| Protective Urgency    | 255, 180, 100 | 230, 120, 40  | 3.0      | 0.15         | 2.5×           | 1.5×              | Sustained      |
| Anxiety / Uncertainty | 220, 200, 160 | 180, 150, 80  | 1.5      | 0.20         | 1.0×           | 2.0× (uneven)     | Sustained      |
| Stern Rebuke          | 255, 255, 220 | 255, 230, 150 | —        | —            | —              | 3.0× (flash)      | ~600ms         |
| Wounded Pride         | (dims 60%)    | (dims 60%)    | 0.7      | 0.15         | 0.5×           | 1.2×              | 3–5s recovery  |
| Quiet Sorrow          | 180, 120, 80  | 120, 70, 40   | 0.4      | 0.10         | 0.3×           | 1.2×              | Until resolved |
| Shrewd Cunning        | 240, 200, 150 | 200, 160, 60  | 1.8      | 0.20         | 1.5×           | 2.0× (elliptical) | Sustained      |
| Pride in Growth       | 255, 230, 170 | 255, 200, 60  | 1.2      | 0.25         | 1.0×           | 3.0× (bloom)      | ~3s            |

---

## What the Ember Does Not Do

**It does not bounce or spin.** The ember is fire, not a ball. Its movement patterns are drift, orbit, jitter, and figure-eight — organic, not mechanical.

**It does not display text.** The ember never shows a label, a percentage, a tooltip, or a badge. It is pre-verbal communication. If you need to know what the ember means, you watch it.

**It does not respond to hover.** On the web and desktop clients, hovering over the ember does nothing. The ember is not a button (except on mobile, where tap-and-hold activates listening). It is not interactive — it is expressive.

**It does not persist a history of states.** The ember is present-tense only. It shows what Virgil is _now_, not what he was. Past states are available through the logging and metrics infrastructure, not through the ember.

**It does not compete with content.** When surfaced content appears below the ember, the ember shrinks (80px → 56px on web) and rises to the top of the viewport. It yields the stage. The content is what the user asked for; the ember is context, not the answer.
