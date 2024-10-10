(ns webapp.connections.views.create-update-connection.hoop-run-instructions
  (:require ["@radix-ui/themes" :refer [Box Grid RadioGroup Text]]
            [clojure.string :as cs]
            [reagent.core :as r]
            [webapp.components.code-snippet :as code-snippet]))

(defn install-hoop []
  (let [select-operation-system (r/atom "macos")]
    (fn []
      [:> Grid {:columns "5" :gap "7"}
       [:> Box {:grid-column "span 2 / span 2"}
        [:> Text {:size "4"
                  :weight "bold"
                  :class "text-[--gray-12]"}
         "Install hoop.dev CLI"]
        [:> Text {:size "3"
                  :as "p"
                  :class "text-[--gray-11]"}
         "Choosing the operating system to connect."]]
       [:> Box {:grid-column "span 3 / span 3" :class "mb-small"}
        [:> RadioGroup.Root {:name (str (name @select-operation-system) "-type")
                             :class "space-y-radix-4"
                             :value @select-operation-system
                             :on-value-change #(reset! select-operation-system %)}
         [:> RadioGroup.Item {:value "macos"} "MacOS"]
         [:> RadioGroup.Item {:value "linux"} "Linux"]]
        [:> Box {:mt "7"}
         [code-snippet/main
          {:id "install-hoop"
           :code (if (= @select-operation-system "macos")
                   (str
                    "brew tap hoophq/brew https://github.com/hoophq/brew\n"
                    "brew install hoop")
                   "curl -s -L https://releases.hoop.dev/release/install-cli.sh | sh")}]]]])))

(defn setup-token [api-key]
  [:> Grid {:columns "5" :gap "7"}
   [:> Box {:grid-column "span 2 / span 2"}
    [:> Text {:size "4"
              :weight "bold"
              :class "text-[--gray-12]"}
     "Setup token"]
    [:> Text {:size "3"
              :as "p"
              :class "text-[--gray-11]"}
     "Export your token to provide a secure connection."]]
   [:> Box {:grid-column "span 3 / span 3" :class "space-y-radix-4"}
    [code-snippet/main
     {:id "setup-token"
      :code (str "export HOOP_KEY=" (:key (:data api-key)))}]
    [:> Text {:size "2" :as "p" :class "text-[--gray-9]"}
     "Do not share this token with anyone outside your organization."]]])

(def hoop-run-commands-dictionary
  {"postgres" "--postgres 'postgres://<user>:<pass>@<host>:<port>/<dbname>'"
   "mysql" "--mysql 'mysql://<user>:<pass>@<host>:<port>/<dbname>'"
   "mssql" "--mssql 'mssql://<user>:<pass>@<host>:<port>/<dbname>'"
   "mongodb" "--mongodb 'mongodb://<user>:<pass>@<host>:<port>/<dbname>'"
   "nodejs" "--command node"
   "ruby-on-rails" "--command 'rails console'"
   "python" "--command python3"
   "clojure" "--command clj"})

(defn run-hoop-connection [{:keys [connection-name
                                   connection-subtype
                                   review?
                                   review-groups
                                   data-masking?
                                   data-masking-fields]}]
  (let [review-command (if review?
                         (str " --review " (cs/join "," review-groups))
                         "")
        data-masking-command (if data-masking?
                               (str " --data-masking " (cs/join "," data-masking-fields))
                               "")]
    [:> Grid {:columns "5" :gap "7"}
     [:> Box {:grid-column "span 2/ span 2" :class "mb-small"}
      [:> Text {:size "4"
                :weight "bold"
                :class "text-[--gray-12]"}
       "Run your hoop connection"]
      [:> Text {:size "3"
                :as "p"
                :class "text-[--gray-11]"}
       "If you have completed all setup steps, you are ready to run and save your connection."]]
     [:> Box {:grid-column "span 3 / span 3"}
      [code-snippet/main
       {:id "run-hoop-connection"
        :code (str "hoop run --name " connection-name " " review-command " "
                   data-masking-command " " (get hoop-run-commands-dictionary connection-subtype))}]]]))
