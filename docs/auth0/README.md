# Auth0 Configuration

Auth0 is a identify provider used to host our **multi tenant instance**. This guide will explain how to customize the screen of Auth0 with Universal Login.

https://auth0.com/docs/customize/login-pages/universal-login/customize-templates

## Install Auth0 cli

- https://github.com/auth0/auth0-cli

## Universal Login Customization

The last command will open a browser locally to customize the template of the Universal Login.

```sh
auth0 login
auth0 universal-login customize
```

1. In the tab `Theme`, paste the contents of the file `template-theme.json`
2. In the tab `Page Layout` paste the contents of the file `template.html`
3. Click on `Deploy Changes`
4. Visit http://localhost:8009 and login to see the changes

## Reference

- [Customize Templates Universal Login](https://auth0.com/docs/customize/login-pages/universal-login/customize-templates)
- https://community.auth0.com/t/add-google-analytics-to-new-universal-login/98994
- https://community.auth0.com/t/adding-google-analytics-to-new-universal-login-experience/44477/9