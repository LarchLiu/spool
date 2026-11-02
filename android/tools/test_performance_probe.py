import json
from pathlib import Path
import struct
import tempfile
import unittest
from unittest.mock import patch

from performance_probe import foreground_snapshot, wait_finished_art_trace


class PerformanceProbeTest(unittest.TestCase):
    def snapshot(self, **changes):
        return {**dict(probeVersion=2, probeToken="12345678-1234-1234-1234-123456789abc", selectionEpoch=3,
                       foreground=True, focused=True, historyVisible=True, historyHasMore=False, turns=64,
                       flingAccepted=False), **changes}

    def dump(self, *values):
        return "TASK\n" + "\n".join("SPOOL_PERF " + json.dumps(value) for value in values) + "\n"

    def test_selects_focused_page_instead_of_first_background_result(self):
        focused = self.snapshot()
        background = self.snapshot(foreground=False, focused=False, turns=25, probeToken="abcdef12-1234-1234-1234-123456789abc")
        self.assertEqual(focused, foreground_snapshot(self.dump(background, focused)))

    def test_never_guesses_when_no_or_multiple_pages_have_focus(self):
        for values in [(self.snapshot(focused=False),), (self.snapshot(), self.snapshot())]:
            with self.assertRaisesRegex(ValueError, "Exactly one"):
                foreground_snapshot(self.dump(*values))

    def test_rejects_legacy_missing_or_malformed_probe_data(self):
        for output in ["", "SPOOL_PERF {invalid}\n", self.dump({"turns": 64})]:
            with self.assertRaises(ValueError):
                foreground_snapshot(output)

    def test_activity_timeout_is_not_misreported_as_an_old_apk(self):
        with self.assertRaisesRegex(ValueError, "responsiveness"):
            foreground_snapshot("Failure while dumping the activity: java.io.IOException: Timeout")

    def test_rejects_navigation_and_activity_changes(self):
        expected = self.snapshot()
        for changes in [dict(selectionEpoch=4), dict(probeToken="abcdef12-1234-1234-1234-123456789abc")]:
            with self.assertRaisesRegex(ValueError, "changed"):
                foreground_snapshot(self.dump(self.snapshot(**changes)), expected)

    def test_requires_a_visible_nonempty_chat_not_settings_or_people(self):
        for changes in [dict(historyVisible=False), dict(turns=0)]:
            with self.assertRaisesRegex(ValueError, "visible"):
                foreground_snapshot(self.dump(self.snapshot(**changes)))

    def test_hydrating_data_requires_explicit_opt_in(self):
        output = self.dump(self.snapshot(historyHasMore=True))
        with self.assertRaisesRegex(ValueError, "hydrating"):
            foreground_snapshot(output)
        self.assertTrue(foreground_snapshot(output, allow_hydrating=True)["historyHasMore"])

    def test_rejected_gestures_cannot_be_reported_as_valid_fling_samples(self):
        with self.assertRaisesRegex(ValueError, "rejected"):
            foreground_snapshot(self.dump(self.snapshot()), require_fling=True)
        self.assertTrue(foreground_snapshot(self.dump(self.snapshot(flingAccepted=True)), require_fling=True)["flingAccepted"])

    def test_requires_valid_target_tokens_and_page_epochs(self):
        for changes in [dict(probeToken=""), dict(selectionEpoch=True), dict(selectionEpoch=-1), dict(selectionEpoch="3")]:
            with self.assertRaises(ValueError):
                foreground_snapshot(self.dump(self.snapshot(**changes)))

    def trace(self, records=1):
        return (b"*version\n3\nclock=dual\n*end\n" + struct.pack("<4sHHQH", b"SLOW", 3, 18, 0, 14)
                + struct.pack("<HIII", 1, 4, 0, 0) * records)

    def wait_trace(self, copies, attempts=4):
        with tempfile.TemporaryDirectory() as directory:
            target = Path(directory) / "trace"
            pending = iter(copies)
            with patch("performance_probe.time.sleep"):
                wait_finished_art_trace(lambda: target.write_bytes(next(pending)), target, attempts=attempts)
            return target.read_bytes()

    def test_waits_for_profile_stop_to_write_the_header_and_complete_records(self):
        trace = self.trace()
        self.assertEqual(trace, self.wait_trace([b"", trace[:-1], trace, trace]))

    def test_a_complete_header_is_not_enough_while_the_trace_is_still_growing(self):
        self.assertEqual(self.trace(3), self.wait_trace([self.trace(), self.trace(2), self.trace(3), self.trace(3)]))

    def test_incomplete_sampling_data_times_out_instead_of_becoming_evidence(self):
        with self.assertRaisesRegex(ValueError, "incomplete sampling data was not used"):
            self.wait_trace([b"unfinished"] * 4)


if __name__ == "__main__":
    unittest.main()
