"""Regression contracts for Steam exceptions, defaults and shared download DNS."""
from pathlib import Path
import unittest

ROOT = Path(__file__).resolve().parents[1]
PROFILE = ROOT / 'subconverter/all-online.ini'

# Representative upstream entries that previously overlapped broad rules.
UPSTREAM = {
    'SteamRegionCheck.list': [('DOMAIN', 'ext1-hkg1.steamserver.net')],
    'SteamCN.list': [('DOMAIN-SUFFIX', d) for d in
                     ('cm.steampowered.com', 'steamusercontent.com', 'steamcontent.com')],
    'Steam.list': [('DOMAIN-SUFFIX', d) for d in
                   ('steampowered.com', 'steamserver.net', 'steamcommunity.com')]
                 + [('DOMAIN', 'steampipe.akamaized.net')],
    'Epic.list': [('DOMAIN-SUFFIX', 'epicgames.com')],
}


class SteamRoutingTest(unittest.TestCase):
    def setUp(self):
        lines = PROFILE.read_text().splitlines()
        self.rules = [line[len('ruleset='):].split(',', 1)
                      for line in lines if line.startswith('ruleset=')]
        self.groups = dict(line[len('custom_proxy_group='):].split('`', 1)
                           for line in lines if line.startswith('custom_proxy_group='))

    def route(self, host):
        for target, source in self.rules:
            entries = ([source[2:].split(',', 1)] if source.startswith('[]')
                       else UPSTREAM.get(source.rsplit('/', 1)[-1], []))
            for kind, value in entries:
                if (kind == 'DOMAIN' and host == value) or (
                    kind == 'DOMAIN-SUFFIX' and (host == value or host.endswith('.' + value))
                ):
                    return target
        return None

    def test_steam_exceptions_win_before_broad_rules(self):
        cases = {
            'ext1-hkg1.steamserver.net': '🎮 Steam 地区检测',
            'other.steamserver.net': '🎮 Steam 出口',
            'cm.steampowered.com': '🎮 Steam 出口',
            'image.steamusercontent.com': '🎮 Steam 出口',
            'cache1.steamcontent.com': '🎮 Steam 下载',
            'steampipe.akamaized.net': '🎮 Steam 下载',
            'store.steampowered.com': '🎮 Steam 出口',
            'steamcommunity.com': '🎮 Steam 出口',
            'ol.epicgames.com': '🖥️ PC游戏平台',
        }
        for host, target in cases.items():
            with self.subTest(host=host):
                self.assertEqual(self.route(host), target)

    def test_defaults_and_references(self):
        for group in ('🎮 Steam 地区检测', '🎮 Steam 下载'):
            self.assertTrue(self.groups[group].startswith('select`[]DIRECT`[]🎮 Steam 出口'))
        self.assertTrue(self.groups['🎮 Steam 出口'].startswith('select`[]🔰 节点选择'))
        for target, _ in self.rules:
            self.assertIn(target, {'DIRECT', 'REJECT'} | self.groups.keys())
        for group in self.groups.values():
            for option in group.split('`')[1:]:
                if option.startswith('[]'):
                    self.assertIn(option[2:], {'DIRECT', 'REJECT'} | self.groups.keys())

    def test_domain_priority(self):
        sources = [source for _, source in self.rules]
        self.assertEqual(len(sources), len(set(sources)))
        first_ip = next(i for i, s in enumerate(sources) if s.endswith('/ChinaIp.list'))
        self.assertLess(next(i for i, s in enumerate(sources) if s.endswith('/ChinaDomain.list')), first_ip)
        self.assertEqual(self.rules[-1], ['🐟 漏网之鱼', '[]FINAL'])

    def test_download_dns_is_narrow_and_explicitly_direct(self):
        template = (ROOT / 'subconverter/server_conf/all_base.tpl').read_text()
        policy = template.split('  nameserver-policy:\n', 1)[1].split('\n  nameserver:', 1)[0]
        self.assertIn('"+.steamcontent.com":', policy)
        self.assertIn('"steampipe.akamaized.net":', policy)
        self.assertIn('"https://223.5.5.5/dns-query#DIRECT"', policy)
        self.assertNotIn('steamusercontent.com', policy)
        self.assertNotIn('"+.steampowered.com"', policy)
        self.assertNotIn('steamserver.net', policy)


if __name__ == '__main__':
    unittest.main()
