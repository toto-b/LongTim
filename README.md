# Local Wavelength Web App (1D & 2D)

A lightweight, zero-dependency local web application based on the party game *Wavelength*. Designed for offline board game nights, couch gaming, or custom tournament events.

No web server or internet connection is required—simply open the HTML files directly in any modern browser.

---

## Features

- **Classic 1D & Custom 2D Grid Modes:** Play the traditional 1-axis version or challenge your team on a 2-axis coordinate system.
- **Randomized Scale Direction:** Binary scales automatically randomize orientation (Left/Right, Top/Bottom) every round.
- **Hotseat Safe:** Dedicated multi-phase flow preventing guessing teams from accidentally seeing hidden target positions.
- **Confirm Lock-in:** Requires explicit confirmation before locking in a guess to avoid accidental clicks.
- **Round History:** Displays scoring logs per round for manual score tracking.
- **Easily Extensible:** Add custom word pairs in seconds via a plain JavaScript config file.

---

## File Structure

- scales.js      # Central array containing binary word pairs
- classic.html   # Classic 1D single-axis game interface
- index.html     # 2D dual-axis grid game interface

---

## Quick Start

1. Download or clone this repository.
2. Double-click classic.html (for 1D mode) or index.html (for 2D mode).
3. Start playing immediately!

---

## How to Add Custom Scales

Open scales.js in any text editor and edit or append new word pairs to the SCALES_DATA array:

const SCALES_DATA = [
  ["Hot", "Cold"],
  ["Boring", "Exciting"],
  ["Underrated", "Overrated"],
  ["Your Custom Left Word", "Your Custom Right Word"]
];

---

## Game Flow

1. **New Round:** Click 'Neue Runde' to select random axis labels and generate a secret target point.
2. **Give Hint:** The hint-giver clicks 'Ziel anzeigen', memorizes the secret position, gives a verbal hint to the team, and clicks 'Ziel verdecken & weiter'.
3. **Make Guess:** The guessing team discusses and clicks on the scale/grid to set a marker.
4. **Lock In:** Click 'Tipp einloggen' to finalize the choice and calculate awarded points based on target proximity.
