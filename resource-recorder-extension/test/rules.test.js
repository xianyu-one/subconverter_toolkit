import test from "node:test";
import assert from "node:assert/strict";
import { cidrRule, hostFromUrl, pageIdentity, ruleOptions, uniqueRuleLines } from "../src/rules.js";

test("URL host and page identity handle IP literals and fragments", () => {
  assert.equal(hostFromUrl("https://[2001:db8::7]/a?x=1"), "2001:db8::7");
  assert.equal(pageIdentity("https://example.com/a?x=1#one"), "https://example.com/a?x=1");
  assert.equal(hostFromUrl("data:text/plain,hello"), null);
});

test("rule options stop at registrable domain and keep exact DOMAIN default", () => {
  const options = ruleOptions("api.shop.example.co.uk", ["203.0.113.7"], () => "example.co.uk");
  assert.deepEqual(options.map((item) => item.text), [
    "DOMAIN,api.shop.example.co.uk",
    "DOMAIN-SUFFIX,api.shop.example.co.uk",
    "DOMAIN-SUFFIX,shop.example.co.uk",
    "DOMAIN-SUFFIX,example.co.uk",
    "IP-CIDR,203.0.113.7/32"
  ]);
  assert.ok(!options.some((item) => item.text.endsWith("co.uk") && item.text === "DOMAIN-SUFFIX,co.uk"));
});

test("literal IP and reported IPv6 use single address CIDR", () => {
  assert.equal(cidrRule("2001:db8::7"), "IP-CIDR,2001:db8::7/128");
  assert.equal(cidrRule("not:an:ip"), null);
  assert.deepEqual(ruleOptions("203.0.113.7").map((item) => item.text), ["IP-CIDR,203.0.113.7/32"]);
  assert.deepEqual(ruleOptions("co.uk", [], () => undefined, () => true).map((item) => item.text), ["DOMAIN,co.uk"]);
});

test("bulk copied rules deduplicate final lines", () => {
  assert.equal(uniqueRuleLines(["DOMAIN,a.example", "DOMAIN,a.example", "DOMAIN,b.example"]), "DOMAIN,a.example\nDOMAIN,b.example");
});
