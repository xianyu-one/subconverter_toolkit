"""Service priority regressions using captured upstream lists and owned rules."""
from pathlib import Path
import tomllib
import unittest

ROOT = Path(__file__).resolve().parents[1]
FIXTURES = Path(__file__).parent / 'fixtures/rulesets'


class ServiceRoutingTest(unittest.TestCase):
    def setUp(self):
        self.rules = [line[8:].split(',', 1) for line in
                      (ROOT / 'subconverter/all-online.ini').read_text().splitlines()
                      if line.startswith('ruleset=')]

    def route(self, host, profile='all-online.ini'):
        rules = self.rules if profile == 'all-online.ini' else [
            line[8:].split(',', 1) for line in
            (ROOT / 'subconverter' / profile).read_text().splitlines()
            if line.startswith('ruleset=')]
        for target, source in rules:
            if source == '[]FINAL':
                return target
            if source.startswith('[]'):
                entries = [source[2:]]
            else:
                name = source.rsplit('/', 1)[-1]
                path = ROOT / 'rule-list' / name if '/xianyu-one/' in source else FIXTURES / name
                entries = path.read_text().splitlines() if path.exists() else []
            for entry in entries:
                parts = entry.strip().split(',')
                if len(parts) < 2 or parts[0].startswith('#'):
                    continue
                kind, value = parts[:2]
                if ((kind == 'DOMAIN' and host == value)
                    or (kind == 'DOMAIN-SUFFIX' and (host == value or host.endswith('.' + value)))
                    or (kind == 'DOMAIN-KEYWORD' and value in host)):
                    return target
        self.fail('Missing final rule')

    def test_douyin_media_is_direct_in_both_profiles(self):
        for profile in ('all-online.ini', 'lite-online.ini'):
            for host in ('v3.douyinvod.click', 'v3.idouyinvod.com',
                         'v3.douyinvideo.net', 'v3.douyinpic.com'):
                with self.subTest(profile=profile, host=host):
                    self.assertEqual(self.route(host, profile), 'DIRECT')
            with self.subTest(profile=profile, host='www.tiktok.com'):
                self.assertNotEqual(self.route('www.tiktok.com', profile), 'DIRECT')

    def test_chrome_update_hosts_use_google_proxy(self):
        hosts = ('tools.google.com', 'dl.google.com',
                 'update.googleapis.com', 'edgedl.me.gvt1.com')
        for profile, target in (('all-online.ini', 'Google'),
                                ('lite-online.ini', '🛫 PASSWALL')):
            for host in hosts:
                with self.subTest(profile=profile, host=host):
                    self.assertEqual(self.route(host, profile), target)

    def test_special_services_win_over_google_and_microsoft(self):
        cases = {
            'youtube.com': '📺 Youtube',
            'music.youtube.com': '📺 Youtube',
            'youtubei.googleapis.com': '📺 Youtube',
            'yt3.ggpht.com': '📺 Youtube',
            'video.googlevideo.com': '📺 Youtube',
            'gemini.google.com': '🤖 AI相关',
            'ai.google.dev': '🤖 AI相关',
            'generativelanguage.googleapis.com': '🤖 AI相关',
            'openaiapi-site.azureedge.net': '🤖 AI相关',
            'chatgpt.com': '🤖 AI相关',
            'www.google.com': 'Google',
            'mtalk.google.com': 'Google',
            'fonts.googleapis.com': 'Google',
            'www.microsoft.com': '🪟 巨硬服务',
            'www.amazon.com': '🛒 Amazon',
            'aiv-cdn.net': '🛒 Amazon',
            'd1v5ir2lpwr8os.cloudfront.net': '🛒 Amazon',
        }
        for host, target in cases.items():
            with self.subTest(host=host):
                self.assertEqual(self.route(host), target)

    def test_existing_services_use_upstream_lists(self):
        for name, target in (('OpenAi.list', '🤖 AI相关'), ('Amazon.list', '🛒 Amazon')):
            self.assertIn([target, 'https://raw.githubusercontent.com/ACL4SSR/ACL4SSR/master/Clash/Ruleset/' + name], self.rules)
        for host, target in (('tenant.auth0.com', '🤖 AI相关'),
                             ('stripe.com', '🤖 AI相关'),
                             ('sentry.io', '🤖 AI相关'),
                             ('challenges.cloudflare.com', '🤖 AI相关'),
                             ('bucket.s3.amazonaws.com', '🛒 Amazon')):
            with self.subTest(host=host):
                self.assertEqual(self.route(host), target)
        self.assertEqual(self.route('unrelated.cloudfront.net'), '🐟 漏网之鱼')
        for profile in ('all-online.ini', 'lite-online.ini'):
            text = (ROOT / 'subconverter' / profile).read_text()
            for removed in ('UnBan.list', 'BanAD.list', 'BanProgramAD.list', '🛑 全球拦截'):
                self.assertNotIn(removed, text)

    def test_supplemental_ip_rules_follow_domain_policies(self):
        sources = [s for _, s in self.rules]
        netflix_ip = next(i for i, s in enumerate(sources) if s.endswith('/NetflixIP.list'))
        for name in ('Microsoft.list', 'Gemini.list', 'ChinaDomain.list', 'Bilibili.list'):
            self.assertLess(next(i for i, s in enumerate(sources) if s.endswith('/' + name)), netflix_ip)
        china_ip = next(i for i, s in enumerate(sources) if s.endswith('/ChinaIp.list'))
        self.assertLess(netflix_ip, china_ip)

    def test_redundant_sources_removed_and_source_limit_respected(self):
        sources = [s for _, s in self.rules]
        for name in ('ClaudeAI.list', 'YouTubeMusic.list', 'CN.list', 'ChinaDNS.list', 'ChinaNet.list'):
            self.assertFalse(any(s.endswith('/' + name) for s in sources), name)
        pref = tomllib.loads((ROOT / 'subconverter/server_conf/pref.toml').read_text())
        limit = pref['advanced']['max_allowed_rulesets']
        self.assertTrue(limit == 0 or len(self.rules) <= limit)

    def test_owned_rule_urls_match_repo_and_files_are_valid(self):
        for profile in ('all-online.ini', 'lite-online.ini'):
            lines = (ROOT / 'subconverter' / profile).read_text().splitlines()
            for line in lines:
                if not line.startswith('ruleset=') or '/xianyu-one/' not in line:
                    continue
                url = line.split(',', 1)[1]
                self.assertTrue(url.startswith('https://raw.githubusercontent.com/xianyu-one/subconverter_configs/main/rule-list/'))
                self.assertTrue((ROOT / 'rule-list' / url.rsplit('/', 1)[-1]).is_file())
        for path in (ROOT / 'rule-list').glob('*.list'):
            text = path.read_text()
            self.assertTrue(text.endswith('\n'), path.name)
            entries = [line for line in text.splitlines() if line and not line.startswith('#')]
            self.assertEqual(len(entries), len(set(entries)), path.name)
            for entry in entries:
                self.assertIn(entry.split(',')[0], ('DOMAIN', 'DOMAIN-SUFFIX'), path.name)


if __name__ == '__main__':
    unittest.main()
