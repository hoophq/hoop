(ns webapp.connections.views.setup.server
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Grid Heading RadioGroup Text]]
   ["lucide-react" :refer [Server Terminal]]
   [re-frame.core :as rf]
   [webapp.connections.views.setup.headers :as headers]))

(def application-types
  [{:id "ruby-on-rails" :title "Ruby on Rails"}
   {:id "python" :title "Python"}
   {:id "nodejs" :title "Node.js"}
   {:id "clojure" :title "Clojure"}])

(defn type-selection-view []
  [:> Box {:class "max-w-2xl mx-auto p-6"}
   [headers/setup-header]

   [:> Box {:class "space-y-5"}
    ;; Connection Type Selection
    [:> Box
     [:> Text {:size "4" :weight "bold" :mb "2"} "Connection type"]
     [:> RadioGroup.Root {:name "server-type"
                          :value @(rf/subscribe [:connection-setup/connection-subtype])
                          :on-value-change #(rf/dispatch [:connection-setup/select-subtype %])}
      [:> Flex {:direction "column" :gap "3"}
       [:> RadioGroup.Item {:value "linux-vm" :class "p-4"}
        [:> Flex {:gap "3" :align "center"}
         [:> Server {:size 16}]
         [:> Box
          [:> Text {:size "3" :weight "bold"} "Linux VM or Container"]
          [:> Text {:size "2" :color "gray"} "Secure shell protocol (SSH) for remote access."]]]]

       [:> RadioGroup.Item {:value "console" :class "p-4"}
        [:> Flex {:gap "3" :align "center"}
         [:> Terminal {:size 16}]
         [:> Box
          [:> Text {:size "3" :weight "bold"} "Console"]
          [:> Text {:size "2" :color "gray"} "For Ruby on Rails, Python, Node JS and more."]]]]]]]

    ;; Application Type (quando console selecionado)
    (when (= @(rf/subscribe [:connection-setup/connection-subtype]) "console")
      [:> Box
       [:> Text {:size "4" :weight "bold" :mb "2"} "Application type"]
       [:> Text {:size "2" :color "gray" :mb "4"}
        "Select stack type for your application connection."]
       [:> RadioGroup.Root {:name "application-type"
                            :value @(rf/subscribe [:connection-setup/app-type])
                            :on-value-change #(rf/dispatch [:connection-setup/select-app-type %])}
        [:> Grid {:columns "2" :gap "3"}
         (for [{:keys [id title]} application-types]
           ^{:key id}
           [:> RadioGroup.Item {:value id :class "p-4"}
            title])]]])

    ;; Operating System Selection (quando app-type selecionado)
    (when @(rf/subscribe [:connection-setup/app-type])
      [:> Box
       [:> Text {:size "4" :weight "bold" :mb "2"} "Operating system"]
       [:> RadioGroup.Root {:name "os-type"
                            :value @(rf/subscribe [:connection-setup/os-type])
                            :on-value-change #(rf/dispatch [:connection-setup/select-os-type %])}
        [:> Grid {:columns "2" :gap "3"}
         [:> RadioGroup.Item {:value "macos" :class "p-4"} "MacOS"]
         [:> RadioGroup.Item {:value "linux" :class "p-4"} "Linux"]]]])]])

(defn installation-view []
  (let [;;connection-name @(rf/subscribe [:connection-setup/connection-name])
        app-type @(rf/subscribe [:connection-setup/app-type])]
    [:> Box {:class "max-w-2xl mx-auto p-6"}
     [headers/console-all-done-header]

     [:> Box {:class "space-y-7"}
      ;; Install CLI
      [:> Box
       [:> Heading {:size "4" :weight "bold" :mb "4"} "Install hoop.dev CLI"]
       [:> Box {:class "bg-gray-900 text-white p-4 rounded-lg font-mono text-sm"}
        "brew tap brew/https://github.com/hoophq/brew"
        [:br]
        "brew install hoop"]]

      ;; Setup Token
      [:> Box
       [:> Heading {:size "4" :weight "bold"} "Setup token"]
       [:> Text {:as "p" :size "3" :mb "4" :class "text-[--gray-11]"}
        "Export your token to provide a secure connection."]
       [:> Box {:class "bg-gray-900 text-white p-4 rounded-lg font-mono text-sm"}
        "export HOOP_KEY=rtk/f1_defaultkey..."]
       [:> Text {:size "2" :class "text-[--gray-9]"}
        "Do not share this token with anyone outside your organization."]]

      ;; Run Connection
      [:> Box
       [:> Heading {:size "4" :weight "bold"} "Run your connection"]
       [:> Text {:as "p" :size "3" :mb "4" :class "text-[--gray-11]"}
        "If you have completed all setup steps, you are ready to run and save your connection."]
       [:> Box {:class "bg-gray-900 text-white p-4 rounded-lg font-mono text-sm"}
        (str "hoop run --name " "aaaa" " --command "
             (case app-type
               "ruby-on-rails" "'rails console'"
               "python" "python3"
               "nodejs" "node"
               "clojure" "clj"
               ""))]]]

     [:> Flex {:justify "end" :mt "6"}
      [:> Button {:size "3"}
       "Done"]]]))

(defn main []
  (let [current-step @(rf/subscribe [:connection-setup/current-step])]
    (if (and (= @(rf/subscribe [:connection-setup/connection-subtype]) "console")
             @(rf/subscribe [:connection-setup/app-type])
             @(rf/subscribe [:connection-setup/os-type]))
      [installation-view]
      [type-selection-view])))
