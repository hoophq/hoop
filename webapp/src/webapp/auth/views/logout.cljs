(ns webapp.auth.views.logout
  (:require
   [re-frame.core :as rf]
   [webapp.components.loaders :as loaders]
   [webapp.routes :as routes]
   [webapp.shell :as shell]))

(defn main []
  ;; Under the React shell, React /logout clears the session. A second write
  ;; here replaced idp-provider-name with "null".
  (when-not (shell/react-shell?)
    (let [auth-method @(rf/subscribe [:gateway->auth-method])
          gateway-info @(rf/subscribe [:gateway->info])
          idp? (not= auth-method "local")]

      (.removeItem js/localStorage "jwt-token")
      (.setItem js/localStorage "idp-provider-name" (get-in gateway-info [:data :idp_provider_name]))

      (js/setTimeout
       #(set! (.. js/window -location -href) (routes/url-for (if idp?
                                                               :idplogin-hoop
                                                               :login-hoop)))
       1500)))

  [loaders/auth-loading-screen {:message "Logging out..."
                                :description "You will be redirected in a few seconds."}])
