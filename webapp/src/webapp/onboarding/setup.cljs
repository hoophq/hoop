(ns webapp.onboarding.setup
  (:require
   ["@radix-ui/themes" :refer [Avatar Box Button Card Flex Heading Text]]
   ["lucide-react" :refer [Database Settings Cloud]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.config :as config]))

(def setup-options
  [{:id "demo"
    :icon (r/as-element
           [:> Database {:size 18 :class "group-hover:text-[--gray-1]"}])
    :title "Explore with a demo database"
    :description "Access a preloaded database to it in action."
    :action #(rf/dispatch [:connections->quickstart-create-postgres-demo])}
   {:id "setup"
    :icon (r/as-element
           [:> Settings {:size 18 :class "group-hover:text-[--gray-1]"}])
    :title "Setup a connection"
    :description "Add your own services or databases."
    :action #(rf/dispatch [:navigate :onboarding-setup-resource])}
   {:id "aws-connect"
    :icon (r/as-element
           [:> Cloud {:size 18 :class "group-hover:text-[--gray-1]"}])
    :title "AWS Connect"
    :description "Access AWS to retrieve and connect your resources."
    :action #(do
               (rf/dispatch [:aws-connect/initialize-state])
               (rf/dispatch [:connection-setup/set-type :aws-connect])
               (rf/dispatch [:navigate :onboarding-aws-connect]))}])

(defn setup-card [{:keys [icon title description action]}]
  [:> Card {:size "1"
            :variant "surface"
            :class "w-full cursor-pointer hover:before:bg-primary-12 group"
            :on-click action}
   [:> Flex {:align "center" :gap "3" :class "group-hover:text-[--gray-1]"}
    [:> Avatar {:size "4"
                :class "group-hover:bg-[--white-a3]"
                :variant "soft"
                :color "gray"
                :fallback icon}]
    [:> Flex {:direction "column"}
     [:> Text {:size "3" :weight "medium" :color "gray-12"} title]
     [:> Text {:size "2" :color "gray-11"} description]]]])

(defn main []
  [:> Flex {:direction "column" :align "center" :justify "center" :class "h-screen"}
   [:> Box {:class "absolute top-0 right-0 p-radix-5"}
    [:> Button {:variant "ghost"
                :size "2"
                :color "gray"
                :on-click #(rf/dispatch [:auth->logout])}
     "Logout"]]

   [:> Box {:class "spacey-y-radix-7 w-[600px]"}
    [:> Box {:class "space-y-radix-6"}

     [:> Box {:class "spacey-y-radix-7 w-[600px]"}
      [:> Box {:class "space-y-radix-6"}
       [:> Box
        [:img {:src (str config/webapp-url "/images/hoop-branding/PNG/hoop-symbol_black@4x.png")
               :class "w-16 mx-auto py-4"}]]

         ;; Title
       [:> Box
        [:> Heading {:as "h1" :align "center" :size "6" :mb "2" :weight "bold" :class "text-[--gray-12]"}
         "How do you want to get started?"]
        [:> Text {:as "p" :size "3" :align "center" :class "text-[--gray-12]"}
         "Choose the setup that works best for you."]]

         ;; Cards
       [:> Box {:class "space-y-radix-4 max-w-[600px]"}
        (for [option setup-options]
          ^{:key (:id option)}
          [setup-card option])]]]]]])

