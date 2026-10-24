## 项目背景
getuploader网站即将停止运营，nico seiga上许多作者的立绘是存储在这个网站上的，本项目旨在尽可能多的抢救这个网站上的立绘（即下载）；

## 项目逻辑
一个基于go语言的命令行工具，可以使用某一个输入作为目录索引（如nico seige本身的搜索框，搜索ボイロ然后以搜索出来的页面为目录，一页一页的，逐个逐个的，点进立绘的页面里查找getuploader的网址和密码\[getuploader的网址是多种多样的，作者留下的密码格式也不固定\]， 然后跳转进此getuploader网址， 输入密码【如有】并进行下载）。

下载下来的文件存储在以 作者名 命名的文件夹里

下载中展示每一个动作的日志，下载后在一个JSON里留下此文件的metadata，包括nico上的网址，nico上的标题,，nico上的作者名，getuploader的网址，文件名，文件路径和哈希值。

进入这个软件的时候，出现`archiver>`的命令行提示标志，输入help可以看到帮助。

每种不同的输入索引对应不同的命令行，目前可以分为以下几种：

1. nico-search {string url} {int page}: 使用nico seiga (如https://seiga.nicovideo.jp/illust/?track=home) 的搜索框作为目录索引进行下载, 可以输入页码跳页， 默认情况下从指定页一直下载下去，没指定的话从第一页开始。
2. nico-author {string url}: 输入一个作者主页地址（如https://seiga.nicovideo.jp/user/illust/39911624），作为索引目录进行下载
3. nico-down {string url}： 输入一个nico seiga投稿页面（如https://seiga.nicovideo.jp/seiga/im11691748）下载
4. down {string url} {string password}:  输入getuploader网页地址（如https://uu.getuploader.com/harinashi/download/71 密码2525）和密码进行下载
5. nico-tag {string tag} {int page}： 使用nico seiga的tag（如 https://seiga.nicovideo.jp/tag/VOICEROID）作为目录索引进行下载, 可以输入跳页， 默认情况下从指定页一直下载下去，没指定的话从第一页开始。
6. down-catalog {string url} {string password} {int page}: 使用getuploader主页 （如 https://ux.getuploader.com/SHINOBILI7/） 作为目录索引进行下载, 可以输入密码（有密码的子页面都使用此密码），可以输入页码跳页（指主页的页码）， 默认情况下从指定页一直下载下去，没指定的话从第一页开始。

其他命令行和要求：
1. cfg 进入设置各种所需令牌的页面， 输入的东西保存在一个json配置文件里
2. 下载路径默认为当前cwd，也可以在cfg里指定
3. 没有指定页码，且有历史记录的情况下，按照历史记录的页码开始下载

## 编写时
如果有需要认证或者令牌的时候问我，我来提供
