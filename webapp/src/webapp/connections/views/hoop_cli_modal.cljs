(ns webapp.connections.views.hoop-cli-modal
  (:require ["@radix-ui/themes" :refer [Box Button Text Flex Tabs]]
            [re-frame.core :as rf]
            [reagent.core :as r]
            [webapp.config :as config]))

(def cli-commands
  {:macos {:install ["brew tap brew https://github.com/hoophq/brew"
                     "brew install hoop"]
           :configure "hoop config create --api-url https://use.hoop.dev"
           :login "hoop login"
           :connect "hoop connect <connection-name>"}
   :linux {:install "curl -s -L https://releases.hoop.dev/release/install-cli.sh | sh"
           :configure "hoop config create --api-url https://use.hoop.dev"
           :login "hoop login"
           :connect "hoop connect <connection-name>"}
   :windows {:docs-link "https://docs.hoop.dev/cli/windows"}})

(defn code-block [commands connection-name]
  [:div {:class "bg-gray-900 text-white p-4 rounded-lg font-mono text-sm space-y-2"}
   (if (vector? commands)
     (for [cmd commands]
       ^{:key cmd}
       [:div (clojure.string/replace cmd "<connection-name>" connection-name)])
     [:div (clojure.string/replace commands "<connection-name>" connection-name)])])

(defn cli-section [title commands connection-name]
  [:div {:class "space-y-3"}
   [:> Text {:as "h3" :size "4" :weight "medium"}
    title]
   [code-block commands connection-name]])

(defn macos-content [connection-name]
  [:div {:class "space-y-6"}
   [cli-section "Install Hoop CLI" (get-in cli-commands [:macos :install]) connection-name]
   [cli-section "Configure authentication" (get-in cli-commands [:macos :configure]) connection-name]
   [cli-section "Get an access token" (get-in cli-commands [:macos :login]) connection-name]
   [cli-section "Connect to resource" (get-in cli-commands [:macos :connect]) connection-name]])

(defn linux-content [connection-name]
  [:div {:class "space-y-6"}
   [cli-section "Install Hoop CLI" (get-in cli-commands [:linux :install]) connection-name]
   [cli-section "Configure authentication" (get-in cli-commands [:linux :configure]) connection-name]
   [cli-section "Get an access token" (get-in cli-commands [:linux :login]) connection-name]
   [cli-section "Connect to resource" (get-in cli-commands [:linux :connect]) connection-name]])

(defn windows-content []
  [:div {:class "space-y-6"}
   [:> Text {:as "h3" :size "4" :weight "medium"}
    "Install Hoop CLI"]
   [:> Text {:as "p" :size "2" :class "text-gray-11"}
    "For details about Windows installation check "
    [:a {:href (get-in cli-commands [:windows :docs-link])
         :target "_blank"
         :class "text-blue-600 hover:text-blue-800 underline"}
     "Windows CLI documentation"]
    "."]])

(defn main [connection-name]
  (let [selected-tab (r/atom "macos")]
    (fn [connection-name]
      [:div {:class "flex max-h-[696px] overflow-hidden -m-8"}
       ;; Left side - Content
       [:div {:class "w-[55%] flex-1 space-y-radix-8 my-10 px-10 overflow-y-auto"}
        [:div {:class "space-y-2"}
         [:> Text {:as "h2" :size "6" :weight "bold"}
          "Get more with Hoop CLI"]
         [:> Text {:as "p" :size "2" :class "text-gray-11"}
          "Follow the steps below to connect to your resource via native access."]]

        [:> Tabs.Root {:value @selected-tab
                       :onValueChange #(reset! selected-tab %)}
         [:> Tabs.List {:class "grid grid-cols-3 gap-1 mb-6"}
          [:> Tabs.Trigger {:value "macos" :class "px-4 py-2 text-sm"}
           "MacOS"]
          [:> Tabs.Trigger {:value "linux" :class "px-4 py-2 text-sm"}
           "Linux"]
          [:> Tabs.Trigger {:value "windows" :class "px-4 py-2 text-sm"}
           "Windows"]]

         [:> Tabs.Content {:value "macos"}
          [macos-content connection-name]]

         [:> Tabs.Content {:value "linux"}
          [linux-content connection-name]]

         [:> Tabs.Content {:value "windows"}
          [windows-content]]]

        [:> Flex {:justify "between"}
         [:> Button {:variant "ghost"
                     :color "gray"
                     :on-click #(rf/dispatch [:modal->close])}
          "Close"]
         [:> Button {:variant "ghost"
                     :color "gray"
                     :on-click #(js/window.open "https://docs.hoop.dev/cli" "_blank")}
          "Hoop CLI Docs"]]]

       ;; Right side - Decorative image
       [:> Box {:class "w-[45%] bg-blue-50"}
        [:img {:src "/images/illustrations/cli-promotion.png"
               :alt "Hoop CLI Modal"
               :class "w-full h-full object-cover"}]]])))
