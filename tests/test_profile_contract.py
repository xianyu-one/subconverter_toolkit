"""Contracts shared by the private-node injector and conversion profiles."""

from pathlib import Path
import re
import unittest


ROOT = Path(__file__).resolve().parents[1]
INJECTOR = ROOT / "prefetch-proxy/internal/privateconfig/config.go"
PROFILE_PATHS = (ROOT / "new.ini", ROOT / "all-online.ini")
DOCKERFILE = ROOT / "Dockerfile"


class ProfileContractTest(unittest.TestCase):
    def test_injected_dialer_group_exists_in_every_clash_profile(self):
        injector = INJECTOR.read_text(encoding="utf-8")
        match = re.search(r'clean\["dialer-proxy"\]\s*=\s*"([^"]+)"', injector)
        self.assertIsNotNone(match, "private-node dialer group was not found")
        dialer_group = match.group(1)

        for profile_path in PROFILE_PATHS:
            with self.subTest(profile=profile_path.name):
                profile = profile_path.read_text(encoding="utf-8")
                groups = {
                    line.split("=", 1)[1].split("`", 1)[0]
                    for line in profile.splitlines()
                    if line.startswith("custom_proxy_group=")
                }
                self.assertIn(dialer_group, groups)
                self.assertIn("🔰 节点选择", groups)
                self.assertIn("🔒 私有出口选择", groups)
                self.assertIn("[]🔒 私有出口选择", profile)

    def test_profiles_are_available_locally_to_subconverter(self):
        dockerfile = DOCKERFILE.read_text(encoding="utf-8")
        for profile_path in PROFILE_PATHS:
            with self.subTest(profile=profile_path.name):
                self.assertIn(
                    f"COPY {profile_path.name} /base/config/{profile_path.name}",
                    dockerfile,
                )


if __name__ == "__main__":
    unittest.main()
