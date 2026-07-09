package policy

import "testing"

func TestResolveAdjudicationDecisionsDown(t *testing.T) {
	cases := []struct {
		name string
		in   AdjudicationInput
		want string
	}{
		{
			name: "operator permanent claim type",
			in:   AdjudicationInput{ClaimType: "decision", Visibility: "operational"},
			want: DispositionOperatorRequired,
		},
		{
			name: "preference is operator permanent",
			in:   AdjudicationInput{ClaimType: "preference", Visibility: "operational"},
			want: DispositionOperatorRequired,
		},
		{
			name: "explicit confirmation",
			in:   AdjudicationInput{PromotionBasis: "explicit_confirmation", Visibility: "operational"},
			want: DispositionOperatorRequired,
		},
		{
			name: "un-evidenced durable",
			in:   AdjudicationInput{Visibility: "operational"},
			want: DispositionDecayPark,
		},
		{
			name: "cheap lane",
			in:   AdjudicationInput{ProvenanceLane: "cheap-lane", Visibility: "operational"},
			want: DispositionDecayPark,
		},
		{
			name: "deterministic import auto-files",
			in:   AdjudicationInput{PromotionBasis: "trusted_deterministic_import", Visibility: "operational"},
			want: DispositionAutoFile,
		},
		{
			name: "private never auto-files",
			in:   AdjudicationInput{PromotionBasis: "trusted_deterministic_import", Visibility: "private"},
			want: DispositionOperatorRequired,
		},
		{
			name: "canon class principle permanent",
			in:   AdjudicationInput{CanonClass: "principle", Visibility: "operational"},
			want: DispositionOperatorRequired,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ResolveAdjudication(tc.in)
			if got.Kind != tc.want {
				t.Fatalf("kind = %q, want %q (reason=%q)", got.Kind, tc.want, got.Reason)
			}
			if got.Kind == DispositionAutoFile && tc.in.Visibility == "private" {
				t.Fatal("private must never auto_file")
			}
			if got.Kind == DispositionDecayPark && got.ParkDays != DefaultDecayParkDays {
				t.Fatalf("park days = %d", got.ParkDays)
			}
		})
	}
}
