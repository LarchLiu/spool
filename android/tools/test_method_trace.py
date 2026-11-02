import json
from pathlib import Path
import struct
import subprocess
import sys
import tempfile
import unittest


class MethodTraceTest(unittest.TestCase):
    def test_dual_clocks_distinguish_main_thread_cpu_from_wall_time(self):
        header = ("*version\n3\nclock=dual\ndata-file-overflow=false\n*threads\n1\tmain\n2\tworker\n"
                  "*methods\n0x4\tcom.cloudgeek.spool.android.Sample\trender\n0x8\tandroid.graphics.Paint\tmeasure\n*end\n").encode()
        binary = struct.pack("<4sHHQH", b"SLOW", 3, 18, 0, 14)
        for row in [(1, 4, 0, 0), (2, 4, 0, 0), (1, 8, 100, 1000), (1, 9, 300, 3000), (1, 5, 400, 5000), (2, 5, 9000, 10000)]:
            binary += struct.pack("<HIII", *row)
        with tempfile.TemporaryDirectory() as directory:
            trace = Path(directory) / "test.trace"
            trace.write_bytes(header + binary)
            output = subprocess.run([sys.executable, str(Path(__file__).with_name("summarize-method-trace.py")), str(trace)],
                                    capture_output=True, text=True, check=True)
        result = json.loads(output.stdout)
        self.assertFalse(result["overflow"])
        self.assertEqual(5, result["sampledMainWallMs"])
        self.assertEqual(.4, result["sampledMainCpuMs"])
        self.assertEqual(2, result["leafWallMs"]["android.graphics.Paint.measure"])
        self.assertEqual(.2, result["leafCpuMs"]["android.graphics.Paint.measure"])
        self.assertEqual(.4, result["appInclusiveCpuMs"]["com.cloudgeek.spool.android.Sample.render"])
        self.assertEqual({"[self]": .2, "android.graphics.Paint.measure": .2},
                         result["appMethodChildrenCpuMs"]["com.cloudgeek.spool.android.Sample.render"])


if __name__ == "__main__":
    unittest.main()
