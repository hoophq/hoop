(ns webapp.connections.views.form.hoop-run-instructions
  (:require ["@headlessui/react" :as ui]
            [clojure.string :as cs]
            [reagent.core :as r]
            [webapp.components.code-snippet :as code-snippet]
            [webapp.components.headings :as h]))

(defn install-hoop []
  (let [select-operation-system (r/atom "macos")]
    (fn []
      [:<>
       [:div {:class "mb-small"}
        [h/h4-md "Install hoop CLI"]
        [:label {:class "text-xs text-gray-500"}
         "Choose the operating system you will connect to"]]
       [:> ui/RadioGroup {:value @select-operation-system
                          :onChange (fn [type]
                                      (reset! select-operation-system type))}
        [:> (.-Label ui/RadioGroup) {:className "sr-only"}
         "Operation systems"]
        [:div {:class "space-y-2"}
         (for [operation-system [{:type "macos" :label "MacOS"}
                                 {:type "linux" :label "Linux"}]]
           ^{:key (:type operation-system)}
           [:> (.-Option ui/RadioGroup)
            {:value (:type operation-system)
             :className (fn [params]
                          (str "relative flex cursor-pointer flex-col rounded-lg border p-4 focus:outline-none md:grid md:grid-cols-3 md:pl-4 md:pr-6 "
                               (if (.-checked params)
                                 "z-10 bg-gray-900"
                                 "border-gray-200")))}
            (fn [params]
              (r/as-element
               [:<>
                [:span {:class "flex items-center text-sm"}
                 [:span {:aria-hidden "true"
                         :class (str "h-4 w-4 rounded-full border bg-white flex items-center justify-center "
                                     (if (.-checked params)
                                       "border-transparent"
                                       "border-gray-300")
                                     (when (.-active params)
                                       "ring-2 ring-offset-2 ring-indigo-600 "))}
                  [:span {:class (str "rounded-full w-1.5 h-1.5 "
                                      (if (.-checked params)
                                        "bg-gray-900"
                                        "bg-white"))}]]
                 [:> (.-Label ui/RadioGroup) {:as "span"
                                              :className (str "ml-3 font-medium "
                                                              (if (.-checked params)
                                                                "text-white"
                                                                "text-gray-700"))}
                  (:label operation-system)]]]))])]]
       [:div {:class "mt-regular"}
        [code-snippet/main
         {:id "install-hoop"
          :code (if (= @select-operation-system "macos")
                  (str
                   "brew tap hoophq/hoopcli https://github.com/hoophq/hoopcli\n"
                   "brew install hoop")
                  "curl -s -L https://releases.hoop.dev/release/install-cli.sh | sh")}]]])))

(defn setup-token [api-key]
  [:<>
   [:div {:class "mb-small"}
    [h/h4-md "Setup the token"]
    [:label {:class "text-xs text-gray-500"}
     "Do not share this token with anyone outside your organization"]]
   [code-snippet/main
    {:id "setup-token"
     :code (str "export HOOP_KEY=" (:key (:data api-key)))}]])

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
    [:<>
     [:div {:class "mb-small"}
      [h/h4-md "Run your hoop connection"]
      [:label {:class "text-xs text-gray-500"}
       "One command away to a life without access headaches"]]
     [code-snippet/main
      {:id "run-hoop-connection"
       :code (str "hoop run --name " connection-name " " review-command " "
                  data-masking-command " " (get hoop-run-commands-dictionary connection-subtype))}]]))
