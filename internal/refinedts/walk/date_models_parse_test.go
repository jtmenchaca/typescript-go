// Covers jsDateParse's offset gate: which Date Time String Format
// spellings name a FIXED time value, and which name a host-dependent
// one and must stay unread.
package walk

import "testing"

// TestA6_edge_json_OffsetAbsentDateTimeFormsStayUnread pins the rule
// sec-date.parse states in its own words: "When the UTC offset
// representation is absent, date-only forms are interpreted as a UTC
// time and date-time forms are interpreted as a local time."
//
// A date-time form with no offset therefore names a different instant
// on every host zone, so jsDateParse must decline it rather than read
// it as UTC. Reading one as UTC let A6.edge.json and A6.edge.process
// fold `new Date("2024-06-01T12:00:00").getTime() - REFERENCE` to
// exactly {0} — a member of Age — so the designated refusal at each
// row's naive-text sink carried no error.
func TestA6_edge_json_OffsetAbsentDateTimeFormsStayUnread(t *testing.T) {
	for _, text := range []string{
		"2024-06-01T12:00:00",
		"2024-06-01T12:00:00.000",
		"2024-06-01T12:00",
	} {
		if _, ok := jsDateParse(text); ok {
			t.Errorf("jsDateParse(%q) read a time value; the offset is absent on a "+
				"date-time form, which sec-date.parse interprets as LOCAL time — "+
				"host-dependent, so no fixed time value exists to read", text)
		}
	}
}

// TestA6_edge_json_OffsetBearingAndDateOnlyFormsRead pins the other
// half of the same clause: a form carrying Z or an explicit offset
// names one instant, and a DATE-ONLY form with the offset absent is
// interpreted as UTC — both are fixed, so both stay read.
func TestA6_edge_json_OffsetBearingAndDateOnlyFormsRead(t *testing.T) {
	cases := map[string]float64{
		// 2024-06-01T12:00:00Z — the offset is explicit
		"2024-06-01T12:00:00Z": 1717243200000,
		// the same instant spelled with a numeric offset
		"2024-06-01T14:00:00+02:00": 1717243200000,
		// date-only: sec-date.parse pins the absent offset to UTC
		"2024-06-01": 1717200000000,
	}
	for text, want := range cases {
		got, ok := jsDateParse(text)
		if !ok {
			t.Errorf("jsDateParse(%q) declined; this form names a fixed time value", text)
			continue
		}
		if got != want {
			t.Errorf("jsDateParse(%q) = %v, want %v", text, got, want)
		}
	}
}
