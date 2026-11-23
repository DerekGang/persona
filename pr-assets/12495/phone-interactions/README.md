# Phone interaction checks · 手机交互对照

These native screenshots complement the 37-page matrix. Each plate states its capture source. The JSON file contains the source and image fingerprints, native bounds and color samples. [Full page gallery](https://github.com/nwverse/persona/blob/f3dbffd7c7f4d6db571c4b9ebedc1d31655ed252/pr-assets/12495/native-9464459a/README.md) · [Evidence](evidence.json)

## Welcome · 首次启动

The four main welcome actions were measured in light, dark and largest text at equal width. The largest edge or dimension difference was 0.63 pt/vp. [Native screenshots](welcome.jpg)

## Agent text selection · 聊天选区

After selecting the fifth historical reply and switching to the largest accessibility size, both native apps kept that reply selected. iPhone presents its selection in a lifted text panel. In this HarmonyOS capture the selected text stays highlighted in the transcript. The iPhone capture used `870924d3`. Its Apple Chat view tree matches the later `a21e4c50` reference. The HarmonyOS before and after sources were `49adc1e9` and `3c69d0f2`. [Native screenshots](selection.jpg)

## Dark sheets · 深色弹层

The native iPhone and revised HarmonyOS sheets use RGB 28, 28, 30 behind their cards and RGB 44, 44, 46 inside. The visual comparison used `9464459a` and `33748351` on HarmonyOS. The four featured components match the public `dc2772d6` source. The keyboard is controlled by the device's input method and has a separate check. [Four native page comparisons](dark-sheets.jpg)

## Subscription meters · 机器订阅用量

The meter with a pacing indicator measured 8 pt/vp on each platform in all three appearances. The dark blue differs by one red-channel level. The hatched area starts at the verified protection threshold. The HarmonyOS capture used `64ccc8f9`. [Native screenshots](subscriptions.png)
