(ns webapp.connections.views.setup.installation
  (:require
   ["@radix-ui/themes" :refer [Box Heading Text]]
   [re-frame.core :as rf]
   [webapp.components.code-snippet :as code-snippet]
   [clojure.string :as cs]))

(def command-dictionary
  {"python" "--command python3"
   "nodejs" "--command node"
   "ruby-on-rails" "--command 'rails console'"
   "clojure" "--command clj"})

(defn get-run-command []
  (let [connection-name @(rf/subscribe [:connection-setup/name])
        app-type @(rf/subscribe [:connection-setup/app-type])
        config @(rf/subscribe [:connection-setup/config])

        review-groups (when (:review config)
                        (map #(get % "value") (:review-groups config)))
        review-command (when (seq review-groups)
                         (str " --review " (cs/join "," review-groups)))
        data-masking-fields (when (:data-masking config)
                              (map #(get % "value") (:data-masking-types config)))
        data-masking-command (when (seq data-masking-fields)
                               (str " --data-masking " (cs/join "," data-masking-fields)))]
    (str "hoop run --name " connection-name
         (when review-command review-command)
         (when data-masking-command data-masking-command)
         " " (get command-dictionary app-type))))

(defn main []
  (rf/dispatch [:organization->get-api-key])
  (fn []
    (let [os-type @(rf/subscribe [:connection-setup/os-type])]
      [:> Box {:class "max-w-[600px] mx-auto space-y-8"}
   ;; Install CLI
       [:> Box {:class "space-y-2"}
        [:> Heading {:size "4" :weight "bold"} "Install hoop.dev CLI"]
        [code-snippet/main
         {:id "install-hoop"
          :code (if (= os-type "macos")
                  (str
                   "brew tap hoophq/brew https://github.com/hoophq/brew\n"
                   "brew install hoop")
                  "curl -s -L https://releases.hoop.dev/release/install-cli.sh | sh")}]]

   ;; Setup token
       [:> Box {:class "space-y-2"}
        [:> Box
         [:> Heading {:size "4" :weight "bold"} "Setup token"]
         [:> Text {:size "3" :class "text-[--gray-11]"}
          "Export your token to provide a secure connection."]]
        [code-snippet/main
         {:id "setup-token"
          :code (str "export HOOP_KEY=" (-> @(rf/subscribe [:organization->api-key]) :data :key))}]
        [:> Text {:size "2" :class "mt-2 text-[--gray-9]"}
         "Do not share this token with anyone outside your organization."]]

   ;; Run your connection
       [:> Box {:class "space-y-2"}
        [:> Box
         [:> Heading {:size "4" :weight "bold"} "Run your connection"]
         [:> Text {:size "3" :class "text-[--gray-11]"}
          "If you have completed all setup steps, you are ready to run and save your connection."]]
        [code-snippet/main
         {:id "run-connection"
          :code (get-run-command)}]]])))
