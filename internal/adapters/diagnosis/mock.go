// Package diagnosis contains adapters for the diagnosis port.
package diagnosis

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"

	"github.com/ipenas-cl/leafwise/internal/domain"
	"github.com/ipenas-cl/leafwise/internal/ports"
)

// Mock is a deterministic diagnoser for tests, CI and demo mode. It does a
// tiny bit of real colour analysis so the demo feels plausible: mostly
// green leaves read as healthy, lots of yellow suggests a nutrient problem,
// brown and crispy suggests underwatering, and so on. Same image in, same
// JSON out, no network, no API key.
type Mock struct{}

func (Mock) Name() string { return "mock" }

type colourStats struct{ green, yellow, brown, pale, dark float64 }

func analyse(img image.Image) colourStats {
	b := img.Bounds()
	step := max(1, max(b.Dx(), b.Dy())/96)
	var s colourStats
	var n float64
	for y := b.Min.Y; y < b.Max.Y; y += step {
		for x := b.Min.X; x < b.Max.X; x += step {
			r16, g16, b16, _ := img.At(x, y).RGBA()
			r, g, bl := float64(r16>>8), float64(g16>>8), float64(b16>>8)
			n++
			switch {
			case r > 200 && g > 200 && bl > 190:
				s.pale++
			case r+g+bl < 120:
				s.dark++
			case g > r*1.1 && g > bl*1.1:
				s.green++
			case r > 150 && g > 130 && bl < 110 && g >= r*0.75:
				s.yellow++
			case r > g && g > bl && r < 200:
				s.brown++
			}
		}
	}
	if n > 0 {
		s.green /= n
		s.yellow /= n
		s.brown /= n
		s.pale /= n
		s.dark /= n
	}
	return s
}

func (Mock) Diagnose(_ context.Context, in ports.DiagnosisInput) (json.RawMessage, error) {
	name := in.CommonName
	if name == "" {
		name = "plant"
	}
	var a domain.Assessment
	img, _, err := image.Decode(bytes.NewReader(in.Image))
	if err != nil {
		// Undecodable (e.g. WebP): fall back to a stable hash-based pick.
		h := sha256.Sum256(in.Image)
		a = scenarios(name)[int(h[0])%len(scenarios(name))]
	} else {
		s := analyse(img)
		sc := scenarios(name)
		switch {
		case s.pale > 0.08 && s.green > 0.2:
			a = sc[1] // powdery mildew
		case s.yellow > 0.18:
			a = sc[2] // nitrogen deficiency / overwatering
		case s.brown > 0.25:
			a = sc[3] // underwatering
		case s.dark > 0.2 && s.green > 0.2:
			a = sc[4] // pests (aphids look like dark specks)
		default:
			a = sc[0]
		}
		a.Findings[0].Evidence = fmt.Sprintf("%s Colour mix: %.0f%% green, %.0f%% yellow, %.0f%% brown.",
			a.Findings[0].Evidence, s.green*100, s.yellow*100, s.brown*100)
	}
	return json.Marshal(a)
}

func scenarios(name string) []domain.Assessment {
	return []domain.Assessment{
		{
			Summary:  fmt.Sprintf("Your %s looks healthy.", name),
			Findings: []domain.Finding{finding(domain.Healthy, "Healthy foliage", 0.86, "Leaves are evenly green with no visible spots or wilting.")},
			Actions:  []domain.Action{action("monitor", "Keep the current watering schedule and check again in two weeks.")},
		},
		{
			Summary: fmt.Sprintf("White powdery patches on the %s suggest powdery mildew.", name),
			Findings: []domain.Finding{
				finding(domain.Disease, "Powdery mildew", 0.78, "Pale, dusty coating on upper leaf surfaces."),
				finding(domain.Overwatering, "Humid, stagnant conditions", 0.31, "Mildew thrives with poor airflow."),
			},
			Actions: []domain.Action{
				action("now", "Remove the worst affected leaves and bin them, do not compost."),
				action("soon", "Spray a 1:10 milk-water solution or potassium bicarbonate every 7 days."),
				action("monitor", "Water at the base in the morning and improve airflow."),
			},
		},
		{
			Summary: fmt.Sprintf("Yellowing leaves on the %s point to a nitrogen shortage, possibly made worse by soggy soil.", name),
			Findings: []domain.Finding{
				finding(domain.NutrientDeficiency, "Nitrogen deficiency", 0.71, "Uniform yellowing, older leaves first."),
				finding(domain.Overwatering, "Possible overwatering", 0.42, "Yellow leaves can also signal waterlogged roots."),
			},
			Actions: []domain.Action{
				action("now", "Check drainage: the top 3 cm of soil should dry between waterings."),
				action("soon", "Feed with a balanced liquid fertilizer at half strength."),
			},
		},
		{
			Summary:  fmt.Sprintf("Brown, crispy edges on the %s indicate underwatering or heat stress.", name),
			Findings: []domain.Finding{finding(domain.Underwatering, "Underwatering", 0.74, "Dry brown margins and curled leaves.")},
			Actions: []domain.Action{
				action("now", "Water deeply until it drains from the bottom of the pot."),
				action("soon", "Add mulch or move out of afternoon sun during heat waves."),
			},
		},
		{
			Summary: fmt.Sprintf("Dark clusters on stems and leaf undersides of the %s look like aphids.", name),
			Findings: []domain.Finding{
				finding(domain.Pest, "Aphids", 0.69, "Small dark insects clustered on new growth."),
			},
			Actions: []domain.Action{
				action("now", "Blast them off with a strong jet of water."),
				action("soon", "Apply insecticidal soap every 5 days for two weeks."),
				action("monitor", "Encourage ladybirds; check new shoots weekly."),
			},
		},
	}
}

func finding(c domain.Condition, label string, conf float64, ev string) domain.Finding {
	return domain.Finding{Condition: c, Label: label, Confidence: conf, Evidence: ev}
}

func action(p, d string) domain.Action { return domain.Action{Priority: p, Description: d} }
