# picgo-plugin-tibi

Uploader for [PicGo](https://github.com/Molunerfinn/PicGo) and
[PicList](https://github.com/Kuingsmile/PicList) that sends images to a
tibi server and returns the direct image link (`/{id}.{ext}`).

Install (until it is published on npm, from this directory):

```sh
picgo install /path/to/tibi/integrations/picgo-plugin
picgo set uploader tibi          # server URL, API token, optional expiry
picgo use uploader tibi
```

In the PicGo/PicList app: Plugin settings → install from a local folder.

Create a token for it on the server:

```sh
tibi admin token create --name picgo --scopes upload
```
