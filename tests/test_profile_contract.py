"""Contracts shared by the private-node injector and conversion profiles."""

from pathlib import Path
import re
import unittest


ROOT = Path(__file__).resolve().parents[1]
INJECTOR = ROOT / "prefetch-proxy/internal/privateconfig/config.go"
PROFILE_PATHS = (ROOT / "all-online.ini", ROOT / "lite-online.ini")
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

    def test_chain_profile_preserves_direct_rules_and_proxies_the_rest(self):
        def rules(path):
            return [
                line.removeprefix("ruleset=").split(",", 1)
                for line in path.read_text(encoding="utf-8").splitlines()
                if line.startswith("ruleset=")
            ]

        base = rules(ROOT / "all-online.ini")
        chain = rules(ROOT / "chain-online.ini")
        self.assertEqual(
            [source for target, source in base if target == "DIRECT"],
            [source for target, source in chain if target == "DIRECT"],
        )
        self.assertTrue(all(target == "DIRECT" for target, _ in chain[:-1]))
        self.assertEqual(chain[-1], ["PASSWALL", "[]FINAL"])
        profile = (ROOT / "chain-online.ini").read_text(encoding="utf-8")
        self.assertIn(
            "custom_proxy_group=PASSWALL`select`[]🔰 节点选择",
            profile,
        )
        self.assertIn(
            "custom_proxy_group=🔰 节点选择`select`[]🔒 私有出口选择`[]✈ 手动选择`[]✈ 延迟最低`[]✈ 故障切换",
            profile,
        )
        for group in ("✈ 手动选择", "✈ 延迟最低", "✈ 故障切换", "🌏 全球直连", "🛑 全球拦截"):
            self.assertIn(f"custom_proxy_group={group}`", profile)


if __name__ == "__main__":
    unittest.main()
