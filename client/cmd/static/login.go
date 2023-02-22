package static

const LoginHTML = `
<!doctype html>
<html lang="en">
  <head>
    <meta charset='utf-8'>
    <meta name="viewport" content="width=device-width,initial-scale=1">
    <title>You logged in successfully at hoop.dev</title>
    <link rel="preconnect" href="https://fonts.googleapis.com">
    <link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
    <link href="https://fonts.googleapis.com/css2?family=Sora:wght@300;400;600;700&display=swap" rel="stylesheet">
    <script src="https://cdn.tailwindcss.com"></script>
    <style>
      body {font-family: 'Sora', sans-serif}
      .main-content {
        background-image: url('https://uploads-ssl.webflow.com/6381011b9a644125428eb040/638114282a33781f01b792de_full-width-one-line.svg');
        background-repeat: no-repeat;
        background-position: left 40%;
        background-size: 100%;
      }
    </style>

    <link rel="icon" type="image/x-icon" href="https://app.hoop.dev/images/hoop-branding/PNG/hoop-symbol_black@4x.png">
  </head>
  <body class="bg-gray-50 h-screen">
    <div class="h-full">
      <div class="flex flex-col gap-4 h-full">
        <header class="container mx-auto py-4">
          <figure class="w-36">
            <img src="https://app.hoop.dev/images/hoop-branding/PNG/hoop-symbol+text_black@4x.png" />
          </figure>
        </header>

        <section class="main-content flex flex-col justify-center place-content-center gap-2 text-center grow">
          <h1 class="text-lg font-bold">
            You're logged-in! 🎉
          </h1>
          <p class="text-sm">
            You can close this page and return to the terminal. It should now be logged in.
          </p>
        </section>

        <footer class="text-xs text-gray-500 py-4 text-center">
          @ hoop.dev 2023 - all rights reserved -
          <a href="https://hoop.dev" class="text-blue-500 underline"> hoop.dev</a>
        </footer>
      </div>
    </div>
  </body>
</html>`
