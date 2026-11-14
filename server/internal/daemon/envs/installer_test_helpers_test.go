package envs

import "context"

// The build helpers install through the real on-demand path; tests give the
// bases a fakeVolume in place of a disk image.
func (b *Bases) buildBaseForTest(ctx context.Context, lang string) error {
	return b.Ensure(ctx, lang)
}

func (b *Bases) buildAllForTest(ctx context.Context) map[string]error {
	errs := map[string]error{}
	for _, lang := range []string{Python, R, Julia} {
		errs[lang] = b.buildBaseForTest(ctx, lang)
	}
	return errs
}

func (b *Bases) buildLeanForTest(ctx context.Context, toolchain, mathlib string) (string, error) {
	return b.EnsureLean(ctx, toolchain, mathlib)
}
