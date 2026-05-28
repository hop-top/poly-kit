package kitinit_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	kitinit "hop.top/kit/cmd/kit/init"
)

func TestParseCopyrights_SingleHolderYearOnly(t *testing.T) {
	got, err := kitinit.ParseCopyrights([]string{"2024 Jane Doe"}, 2026)
	require.NoError(t, err)
	assert.Equal(t, []kitinit.Copyright{
		{Years: "2024", Holder: "Jane Doe", URL: ""},
	}, got)
}

func TestParseCopyrights_SingleHolderWithURL(t *testing.T) {
	got, err := kitinit.ParseCopyrights(
		[]string{"2024 Jane Doe <https://jane.example>"}, 2026)
	require.NoError(t, err)
	assert.Equal(t, []kitinit.Copyright{
		{Years: "2024", Holder: "Jane Doe", URL: "https://jane.example"},
	}, got)
}

func TestParseCopyrights_YearRange(t *testing.T) {
	got, err := kitinit.ParseCopyrights(
		[]string{"2020-2024 Acme Inc <https://acme.example>"}, 2026)
	require.NoError(t, err)
	assert.Equal(t, []kitinit.Copyright{
		{Years: "2020-2024", Holder: "Acme Inc", URL: "https://acme.example"},
	}, got)
}

func TestParseCopyrights_MultipleHoldersSemicolon(t *testing.T) {
	got, err := kitinit.ParseCopyrights(
		[]string{"2020 Foo <https://foo.io>;2024 Bar"}, 2026)
	require.NoError(t, err)
	assert.Equal(t, []kitinit.Copyright{
		{Years: "2020", Holder: "Foo", URL: "https://foo.io"},
		{Years: "2024", Holder: "Bar", URL: ""},
	}, got)
}

func TestParseCopyrights_MultipleHoldersRepeatedFlag(t *testing.T) {
	got, err := kitinit.ParseCopyrights(
		[]string{"2020 Foo <https://foo.io>", "2024 Bar"}, 2026)
	require.NoError(t, err)
	assert.Equal(t, []kitinit.Copyright{
		{Years: "2020", Holder: "Foo", URL: "https://foo.io"},
		{Years: "2024", Holder: "Bar", URL: ""},
	}, got)
}

func TestParseCopyrights_MixedRepeatedFlagAndSemicolon(t *testing.T) {
	got, err := kitinit.ParseCopyrights(
		[]string{"2020 Foo;2021 Bar", "2024 Baz <https://baz.io>"}, 2026)
	require.NoError(t, err)
	assert.Equal(t, []kitinit.Copyright{
		{Years: "2020", Holder: "Foo", URL: ""},
		{Years: "2021", Holder: "Bar", URL: ""},
		{Years: "2024", Holder: "Baz", URL: "https://baz.io"},
	}, got)
}

func TestParseCopyrights_LegacySingleName(t *testing.T) {
	got, err := kitinit.ParseCopyrights([]string{"Jane Doe"}, 2026)
	require.NoError(t, err)
	assert.Equal(t, []kitinit.Copyright{
		{Years: "2026", Holder: "Jane Doe", URL: ""},
	}, got)
}

func TestParseCopyrights_EmptyChunksSkipped(t *testing.T) {
	got, err := kitinit.ParseCopyrights(
		[]string{";2024 Foo;;2025 Bar;"}, 2026)
	require.NoError(t, err)
	assert.Equal(t, []kitinit.Copyright{
		{Years: "2024", Holder: "Foo", URL: ""},
		{Years: "2025", Holder: "Bar", URL: ""},
	}, got)
}

func TestParseCopyrights_ReversedRangeErrors(t *testing.T) {
	_, err := kitinit.ParseCopyrights([]string{"2022-2016 Acme"}, 2026)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reversed year range")
}

func TestParseCopyrights_MissingClosingAngle(t *testing.T) {
	_, err := kitinit.ParseCopyrights(
		[]string{"2024 Jane <https://jane.example"}, 2026)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing closing '>'")
}

func TestParseCopyrights_MissingHolderAfterYears(t *testing.T) {
	_, err := kitinit.ParseCopyrights([]string{"2024  "}, 2026)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing holder name")
}

func TestDefaultCopyrights_FourHolderBlock(t *testing.T) {
	got := kitinit.DefaultCopyrights(2026)
	require.Len(t, got, 4)
	assert.Equal(t, "2026", got[0].Years)
	assert.Equal(t, "Idea Crafters LLC", got[0].Holder)
	assert.Equal(t, "https://ideacrafters.com", got[0].URL)
	assert.Equal(t, "AI Experts", got[1].Holder)
	assert.Equal(t, "https://lesexperts.ai", got[1].URL)
	assert.Equal(t, "@jadb", got[2].Holder)
	assert.Equal(t, "https://github.com/jadb", got[2].URL)
	assert.Equal(t, "@monaam", got[3].Holder)
	assert.Equal(t, "https://github.com/monaam", got[3].URL)
}
