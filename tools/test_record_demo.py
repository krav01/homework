import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

from record_demo import replay_page


class RecorderTest(unittest.TestCase):
    def test_actual_output_and_exit_status(self):
        for exit_code in (0, 7):
            with self.subTest(exit_code=exit_code), tempfile.TemporaryDirectory() as directory:
                output = Path(directory) / "demo"
                result = subprocess.run(
                    [sys.executable, str(Path(__file__).with_name("record_demo.py")),
                     "--output", str(output), "--", sys.executable, "-c",
                     f"import sys; print('actual output'); sys.exit({exit_code})"],
                    capture_output=True, text=True, check=False)
                self.assertEqual(result.returncode, exit_code)
                data = [json.loads(line) for line in output.with_suffix(".cast").read_text().splitlines()]
                self.assertEqual(data[0]["version"], 2)
                self.assertIn("actual output", data[1][2])
                self.assertIn(str(exit_code), data[-1][2])
                page = output.with_suffix(".html").read_text()
                self.assertIn("PASSED" if exit_code == 0 else "FAILED (exit 7)", page)

    def test_output_cannot_inject_script(self):
        attack = "</script><script>alert(1)</script>"
        page = replay_page({"title": attack}, [[0, "o", attack]], 0)
        self.assertNotIn(attack, page)
        self.assertIn("\\u003c/script>", page)


if __name__ == "__main__":
    unittest.main()
