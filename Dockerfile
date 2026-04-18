FROM tindy2013/subconverter:latest

COPY subconverter_server_conf/include /base/include
COPY subconverter_server_conf/all_base.tpl /base/base/all_base.tpl
COPY subconverter_server_conf/emoji.toml /base/snippets/emoji.toml
COPY subconverter_server_conf/pref.toml /base/pref.toml