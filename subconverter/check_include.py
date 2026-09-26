
#!/usr/bin/env python3
# -*- coding: utf-8 -*-

"""
Subconverter Include 文件检查工具

功能：
1. 交互式选择目录，支持 Tab 路径补全。
2. 检查目录内所有 TXT 文件的换行格式。
3. 检查文件末尾是否存在 LF 换行符。
4. 检查 BOM、Tab 缩进、空白行。
5. 检测文件内部及跨文件重复域名。
6. 生成完整检查报告。
7. 可选修复末尾换行符，自动备份原文件。

运行环境：Python 3.9+
仅使用 Python 标准库。
"""

import os
import re
import glob
import shutil
from pathlib import Path
from collections import defaultdict, Counter
from datetime import datetime

try:
    import readline

    def path_completer(text, state):
        expanded = os.path.expanduser(text)

        matches = sorted(
            glob.glob(glob.escape(expanded) + "*")
        )

        results = []

        for match in matches:
            if os.path.isdir(match):
                match += "/"

            if text.startswith("~"):
                home = os.path.expanduser("~")

                if match.startswith(home):
                    match = "~" + match[len(home):]

            results.append(match)

        if state < len(results):
            return results[state]

        return None

    readline.set_completer(path_completer)
    readline.parse_and_bind("tab: complete")
    readline.set_completer_delims("\n")

except ImportError:
    print("提示：当前环境不支持 Tab 路径补全。")


def ask_directory():
    """交互式输入目录"""

    while True:
        value = input(
            "\n请输入 include 文件夹路径："
        ).strip()

        if value.lower() in ("q", "quit", "exit"):
            return None

        value = os.path.expandvars(
            os.path.expanduser(value)
        )

        path = Path(value)

        if not path.exists():
            print("错误：目录不存在")
            continue

        if not path.is_dir():
            print("错误：输入路径不是目录")
            continue

        return path.resolve()


def normalize_domain(line):
    """
    提取并归一化 YAML 列表项。

    只移除：
    - 行首空白
    - YAML 列表标记
    - 首尾空白
    - 成对引号
    - 大小写差异

    不改变通配符语义。
    """

    text = line.strip()

    if not text:
        return None

    if text.startswith("#"):
        return None

    match = re.match(r"^-\s+(.+)$", text)

    if not match:
        return None

    domain = match.group(1).strip()

    if (
        len(domain) >= 2
        and domain[0] == domain[-1]
        and domain[0] in ("'", '"')
    ):
        domain = domain[1:-1]

    domain = domain.strip().lower()

    return domain or None


def analyze_file(path, domain_index):
    """检查单个文件"""

    data = path.read_bytes()

    result = {
        "path": path,
        "size": len(data),
        "total_lines": 0,
        "lf": 0,
        "crlf": 0,
        "cr_only": 0,
        "final_newline": False,
        "bom": False,
        "blank_lines": [],
        "tab_lines": [],
        "invalid_lines": [],
        "duplicate_domains": {},
        "domain_count": 0,
    }

    result["bom"] = data.startswith(b"\xef\xbb\xbf")

    result["crlf"] = data.count(b"\r\n")

    result["lf"] = (
        data.count(b"\n") - result["crlf"]
    )

    result["cr_only"] = (
        data.count(b"\r") - result["crlf"]
    )

    # 文件末尾必须是 LF
    result["final_newline"] = data.endswith(b"\n")

    text = data.decode(
        "utf-8-sig",
        errors="replace"
    )

    lines = text.splitlines()

    result["total_lines"] = len(lines)

    local_domains = defaultdict(list)

    for lineno, line in enumerate(lines, 1):

        if not line.strip():
            result["blank_lines"].append(lineno)
            continue

        if line.startswith("\t") or re.match(
            r"^[ \t]*\t", line
        ):
            result["tab_lines"].append(lineno)

        domain = normalize_domain(line)

        if domain is None:
            if not line.lstrip().startswith("#"):
                result["invalid_lines"].append(lineno)

            continue

        result["domain_count"] += 1

        local_domains[domain].append(lineno)

        domain_index[domain].append(
            (path.name, lineno)
        )

    # 文件内重复规则
    result["duplicate_domains"] = {
        domain: linenos
        for domain, linenos in local_domains.items()
        if len(linenos) > 1
    }

    return result


def format_lines(lines, limit=20):
    """限制终端输出长度"""

    if not lines:
        return "无"

    preview = ", ".join(
        map(str, lines[:limit])
    )

    if len(lines) > limit:
        preview += (
            f" ... 另有 {len(lines) - limit} 行"
        )

    return preview


def print_file_report(result):
    """输出单文件报告"""

    path = result["path"]

    print("\n" + "=" * 72)
    print(f"文件：{path.name}")
    print("=" * 72)

    print(f"文件大小：{result['size']:,} 字节")
    print(f"总行数：{result['total_lines']:,}")
    print(f"有效规则数：{result['domain_count']:,}")

    print("\n【换行检查】")

    print(f"LF 换行：{result['lf']:,}")
    print(f"CRLF 换行：{result['crlf']:,}")
    print(f"孤立 CR：{result['cr_only']:,}")

    if result["final_newline"]:
        print("文件末尾换行：正常")
    else:
        print("文件末尾换行：异常（缺少 LF）")

    print(
        "UTF-8 BOM："
        + ("存在" if result["bom"] else "无")
    )

    print("\n【格式检查】")

    print(
        "空白行："
        + format_lines(result["blank_lines"])
    )

    print(
        "Tab 缩进："
        + format_lines(result["tab_lines"])
    )

    print(
        "非标准列表项："
        + format_lines(result["invalid_lines"])
    )

    print("\n【文件内部重复】")

    duplicates = result["duplicate_domains"]

    if not duplicates:
        print("未发现完全重复的域名规则。")
    else:
        print(
            f"重复规则种类：{len(duplicates):,}"
        )

        count = 0

        for domain, linenos in sorted(
            duplicates.items()
        ):
            print(
                f"  {domain} "
                f"（行号：{format_lines(linenos)}）"
            )

            count += 1

            if count >= 20:
                break

        if len(duplicates) > 20:
            print(
                f"  ... 另有 "
                f"{len(duplicates) - 20:,} "
                f"种重复规则未在终端显示"
            )


def find_cross_file_duplicates(domain_index):
    """检测跨文件重复规则"""

    duplicates = {}

    for domain, locations in domain_index.items():

        filenames = {
            filename
            for filename, _ in locations
        }

        if len(filenames) > 1:
            duplicates[domain] = locations

    return duplicates


def write_report(
    directory,
    results,
    cross_duplicates
):
    """生成完整报告"""

    timestamp = datetime.now().strftime(
        "%Y%m%d_%H%M%S"
    )

    report_path = directory / (
        f"include_check_{timestamp}.report"
    )

    with report_path.open(
        "w",
        encoding="utf-8"
    ) as f:

        f.write(
            "Subconverter Include 检查报告\n"
        )

        f.write(
            "=" * 72 + "\n\n"
        )

        for result in results:

            f.write(
                f"文件：{result['path'].name}\n"
            )

            f.write(
                f"总行数：{result['total_lines']}\n"
            )

            f.write(
                f"规则数：{result['domain_count']}\n"
            )

            f.write(
                "末尾换行："
                + (
                    "正常"
                    if result["final_newline"]
                    else "缺失"
                )
                + "\n"
            )

            f.write(
                f"LF：{result['lf']}，"
                f"CRLF：{result['crlf']}，"
                f"CR：{result['cr_only']}\n"
            )

            f.write(
                "空白行："
                + str(result["blank_lines"])
                + "\n"
            )

            f.write(
                "Tab 行："
                + str(result["tab_lines"])
                + "\n"
            )

            f.write(
                "非标准列表项："
                + str(result["invalid_lines"])
                + "\n"
            )

            f.write("\n文件内部重复：\n")

            for domain, linenos in sorted(
                result["duplicate_domains"].items()
            ):
                f.write(
                    f"{domain}: {linenos}\n"
                )

            f.write("\n" + "-" * 72 + "\n\n")

        f.write("\n跨文件重复规则：\n")

        for domain, locations in sorted(
            cross_duplicates.items()
        ):

            locations_text = ", ".join(
                f"{filename}:{lineno}"
                for filename, lineno in locations
            )

            f.write(
                f"{domain}: {locations_text}\n"
            )

    return report_path


def fix_final_newlines(results):
    """
    修复文件末尾缺少 LF 的问题。

    仅对缺少 LF 的文件追加一个换行符。
    修改前创建备份。
    """

    targets = [
        result["path"]
        for result in results
        if (
            result["size"] > 0
            and not result["final_newline"]
        )
    ]

    if not targets:
        print("\n没有需要修复的文件。")
        return

    print("\n以下文件缺少末尾换行符：")

    for path in targets:
        print(f"  {path.name}")

    answer = input(
        "\n是否自动修复？"
        "将自动备份原文件 [y/N]："
    ).strip().lower()

    if answer not in ("y", "yes"):
        print("已取消修复。")
        return

    timestamp = datetime.now().strftime(
        "%Y%m%d_%H%M%S"
    )

    for path in targets:

        backup = path.with_name(
            path.name + f".bak.{timestamp}"
        )

        # 避免覆盖已有备份
        suffix = 1

        while backup.exists():
            backup = path.with_name(
                path.name
                + f".bak.{timestamp}.{suffix}"
            )
            suffix += 1

        shutil.copy2(path, backup)

        with path.open("ab") as f:
            f.write(b"\n")

        print(
            f"已修复：{path.name}\n"
            f"备份文件：{backup.name}"
        )


def main():

    print("=" * 72)
    print("Subconverter Include 文件检查工具")
    print("=" * 72)

    print("支持目录路径 Tab 补全。")
    print("扫描指定目录下所有 .txt 文件。")
    print("默认只读，修复操作需要单独确认。")

    directory = ask_directory()

    if directory is None:
        return

    files = sorted(
        path
        for path in directory.iterdir()
        if (
            path.is_file()
            and path.suffix.lower() == ".txt"
        )
    )

    if not files:
        print("指定目录下没有找到 TXT 文件。")
        return

    print(
        f"\n找到 {len(files)} 个 TXT 文件。"
    )

    domain_index = defaultdict(list)

    results = []

    for path in files:

        try:
            result = analyze_file(
                path,
                domain_index
            )

            results.append(result)

            print_file_report(result)

        except (OSError, UnicodeError) as e:
            print(
                f"\n文件读取失败：{path.name}"
                f"\n错误：{e}"
            )

    cross_duplicates = (
        find_cross_file_duplicates(domain_index)
    )

    print("\n" + "=" * 72)
    print("【跨文件重复规则统计】")
    print("=" * 72)

    print(
        f"跨文件重复规则种类："
        f"{len(cross_duplicates):,}"
    )

    for domain, locations in list(
        sorted(cross_duplicates.items())
    )[:20]:

        locations_text = ", ".join(
            f"{filename}:{lineno}"
            for filename, lineno in locations
        )

        print(
            f"{domain} -> {locations_text}"
        )

    if len(cross_duplicates) > 20:
        print(
            f"... 另有 "
            f"{len(cross_duplicates) - 20:,} "
            f"种重复规则未在终端显示"
        )

    report_path = write_report(
        directory,
        results,
        cross_duplicates
    )

    print("\n" + "=" * 72)
    print("检查完成")
    print("=" * 72)

    print(f"完整报告：{report_path}")

    fix_final_newlines(results)


if __name__ == "__main__":
    main()