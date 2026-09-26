# 離線地圖資料來源

地圖輪廓 `internal/api/web/world.geojson` 由 [Natural Earth 1:110m Admin 0 GeoJSON](https://github.com/nvkelso/natural-earth-vector/blob/master/geojson/ne_110m_admin_0_countries.geojson) 擷取幾何欄位而成。`internal/geo/countries.json` 擷取該資料的 ISO 國家代碼、名稱與標籤位置；`internal/geo/regions.json` 擷取 [Natural Earth 1:10m Admin 1 GeoJSON](https://github.com/nvkelso/natural-earth-vector/blob/master/geojson/ne_10m_admin_1_states_provinces.geojson) 的 ISO 省州代碼、名稱與標籤座標。資料於 2026-09-26 取得。Natural Earth 為[公有領域資料](https://github.com/nvkelso/natural-earth-vector)，此倉庫只打包上述精簡結果。

省州資料不保證每個地區都具有 ISO 代碼和示意點。缺少對應位置時，Observer 退回國家級估計或顯示未知。地圖線條不表示地理網路路由。
